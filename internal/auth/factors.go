package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Auth factors: non-interactive sign-in methods (API keys today; device
// pubkeys and OAuth2 providers later — the schema already reserves them).
// A user may have several methods; interactive login keeps using
// users.pass_hash. Device-only users get a random password hash they can
// never know, so /auth/login structurally cannot succeed for them.

type Factor struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Method     string     `json:"method"`
	Alg        string     `json:"alg,omitempty"`
	Label      string     `json:"label,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"` // nil = never
	Expired    bool       `json:"expired"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// ErrFactorExists fires when (user, method, label) is already taken.
var ErrFactorExists = errors.New("auth factor already exists")

// randomKey returns 32 random bytes as 43 URL-safe characters (no padding,
// copy-pasteable into device firmware config).
func randomKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashKey maps an api key to its stored form. Keys are 256-bit random, so a
// plain sha256 is the right call (same model as session tokens); bcrypt
// would only slow down our own per-request lookups.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// CreateAPIKey generates a fresh API key for userID and returns the factor
// plus the plaintext key — the plaintext exists only in this response, the
// DB keeps just the hash.
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, label string, expiresAt *time.Time) (Factor, string, error) {
	key, err := randomKey()
	if err != nil {
		return Factor{}, "", err
	}
	var exp any
	if expiresAt != nil {
		exp = expiresAt.Unix()
	}
	res, err := s.DB.ExecContext(ctx,
		"INSERT INTO auth_factors (user_id, method, label, secret, created_at, expires_at) VALUES (?,?,?,?,?,?)",
		userID, "apikey", label, hashKey(key), time.Now().UTC().Format(tsFmt), exp)
	if err != nil {
		if isUniqueErr(err) {
			return Factor{}, "", ErrFactorExists
		}
		return Factor{}, "", err
	}
	id, _ := res.LastInsertId()
	f := Factor{ID: id, UserID: userID, Method: "apikey", Label: label, CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt}
	return f, key, nil
}

func (s *Store) ListFactors(ctx context.Context, userID int64) ([]Factor, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, user_id, method, alg, label, created_at, expires_at, last_used_at
		 FROM auth_factors WHERE user_id = ? ORDER BY method, label`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Factor{}
	now := time.Now().Unix()
	for rows.Next() {
		var (
			f       Factor
			caStr   string
			exp, lu sql.NullInt64
		)
		if err := rows.Scan(&f.ID, &f.UserID, &f.Method, &f.Alg, &f.Label, &caStr, &exp, &lu); err != nil {
			return nil, err
		}
		f.CreatedAt, _ = time.Parse(tsFmt, caStr)
		if exp.Valid {
			t := time.Unix(exp.Int64, 0).UTC()
			f.ExpiresAt = &t
			f.Expired = exp.Int64 <= now
		}
		if lu.Valid {
			t := time.Unix(lu.Int64, 0).UTC()
			f.LastUsedAt = &t
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFactor revokes a factor; effective on the next request (bearer
// lookups hit the table directly, no session to expire).
func (s *Store) DeleteFactor(ctx context.Context, factorID int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM auth_factors WHERE id = ?", factorID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UserByAPIKey resolves a bearer token to its user. Unknown/expired keys and
// expired accounts all collapse to ErrNotFound. last_used_at feeds fleet
// health ("is this device still alive?") without a write on every hit when
// it was updated recently.
func (s *Store) UserByAPIKey(ctx context.Context, key string) (User, error) {
	if key == "" || len(key) > 128 {
		return User{}, ErrNotFound
	}
	var (
		factorID int64
		userID   int64
		lastUsed sql.NullInt64
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, user_id, last_used_at FROM auth_factors
		 WHERE method = 'apikey' AND secret = ? AND (expires_at IS NULL OR expires_at > ?)`,
		hashKey(key), time.Now().Unix()).Scan(&factorID, &userID, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	if u.Expired || !u.Active { // account-level lapse/suspension outranks a live key
		return User{}, ErrNotFound
	}
	if !lastUsed.Valid || lastUsed.Int64 < time.Now().Add(-time.Minute).Unix() {
		s.DB.ExecContext(ctx, "UPDATE auth_factors SET last_used_at = ? WHERE id = ?", time.Now().Unix(), factorID)
	}
	return u, nil
}

// isUniqueErr detects SQLite unique-constraint violations for friendly errors.
func isUniqueErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint")
}
