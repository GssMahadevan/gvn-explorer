package auth

// Device pairing: an admin opens a short-lived invite (rendered as a QR
// code or a 7-character short code), the joining device redeems it at
// POST /api/v1/join, and a plain account is created in that tenant on the
// spot. Claim secrets are stored hashed (same model as API keys, see
// hashKey in factors.go), so a copied perms.db never leaks usable
// invites. Invites are single-use and self-expiring; unused ones can be
// revoked, which stamps revoked_at instead of deleting the row.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/query"
)

// Pairing is one join invite as the API exposes it. The claim token and
// short code only ever appear in the create response; every later read
// shows metadata and status, never anything redeemable.
type Pairing struct {
	ID          int64      `json:"id"`
	TenantID    int64      `json:"tenant_id"`
	Tenant      string     `json:"tenant"`
	Description string     `json:"description"`
	Username    string     `json:"username,omitempty"` // preset login; "" = derived at join
	APIKey      bool       `json:"apikey"`             // an API key is issued on join
	UserExpires *time.Time `json:"user_expires_at,omitempty"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
	UsedBy      string     `json:"used_by,omitempty"` // joined username once redeemed
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	Status      string     `json:"status"` // pending | used | revoked | expired (derived)
}

// Redeem result errors for the join endpoint's distinct answers.
var (
	ErrPairNotFound = errors.New("no such pairing")
	ErrPairExpired  = errors.New("pairing expired")
	ErrPairUsed     = errors.New("pairing already used or revoked")
	ErrUserTaken    = errors.New("username already exists in tenant")
)

const (
	// PairingTTLDefault is the invite lifetime when the caller doesn't say.
	PairingTTLDefault = 10 * time.Minute
	// PairingTTLMax bounds how long an invite may stay open at all.
	PairingTTLMax = 24 * time.Hour
)

// crockford is the Crockford base32 alphabet: no I/L/O/U, so short codes
// survive being read aloud or retyped from a screen.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// randomCode draws n characters from the Crockford alphabet. 7 characters
// are ~3.5e10 possibilities; combined with the 10-minute TTL, single use
// and the per-IP limiter, brute force against one live invite is hopeless.
func randomCode(n int) (string, error) {
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(crockford))))
		if err != nil {
			return "", err
		}
		b[i] = crockford[v.Int64()]
	}
	return string(b), nil
}

// deriveDeviceUsername invents a login for invites without a preset
// username: "dev-" plus 5 unambiguous lowercase characters.
func deriveDeviceUsername() string {
	for {
		s, err := randomCode(5)
		if err != nil {
			return "dev-x" // unreachable; crypto/rand does not fail here
		}
		return "dev-" + strings.ToLower(s)
	}
}

// CreatePairing opens an invite inside tenantID and returns the stored row
// plus the two claim secrets (token for the QR, code for typing).
// username, when set, is the login the join will create — it must not
// collide with an existing account (ErrUserTaken).
func (s *Store) CreatePairing(ctx context.Context, tenantID int64, description, username string, apikey bool, userExpires *time.Time, ttl time.Duration, createdBy int64) (Pairing, string, string, error) {
	if username != "" {
		var dup int
		err := s.DB.QueryRowContext(ctx, "SELECT 1 FROM users WHERE tenant_id = ? AND username = ?", tenantID, username).Scan(&dup)
		if err == nil {
			return Pairing{}, "", "", ErrUserTaken
		}
	}
	if ttl <= 0 {
		ttl = PairingTTLDefault
	}
	now := time.Now().UTC()
	p := Pairing{
		TenantID: tenantID, Description: description, Username: username, APIKey: apikey,
		UserExpires: userExpires, CreatedAt: now, ExpiresAt: now.Add(ttl), Status: "pending",
	}
	// token/code collisions are astronomically unlikely, but the UNIQUE
	// indexes are the arbiter: retry once if we somehow hit one.
	for attempt := 0; attempt < 3; attempt++ {
		token, err := randomKey()
		if err != nil {
			return Pairing{}, "", "", err
		}
		code, err := randomCode(7)
		if err != nil {
			return Pairing{}, "", "", err
		}
		var exp any
		if userExpires != nil {
			exp = userExpires.Unix()
		}
		id, err := s.DB.Insert(ctx,
			"INSERT INTO pairings (tenant_id, token_hash, code_hash, description, username, apikey, user_expires_at, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
			tenantID, hashKey(token), hashKey(code), description,
			nilIfEmpty(username), b2i(apikey), exp, createdBy, now.Format(tsFmt), p.ExpiresAt.Unix())
		if isUniqueErr(err) {
			continue
		}
		if err != nil {
			return Pairing{}, "", "", err
		}
		p.ID = id
		return p, token, code, nil
	}
	return Pairing{}, "", "", errors.New("could not generate a unique pairing code")
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// pairCols never exposes token_hash/code_hash — knowing a pairing exists
// must never be enough to redeem it.
const pairCols = `p.id, p.tenant_id, t.name, p.description, p.username, p.apikey, p.user_expires_at,
	COALESCE(cb.username, ''), p.created_at, p.expires_at,
	p.used_at, COALESCE(ju.username, ''), p.revoked_at`
const pairFrom = `FROM pairings p
	JOIN tenants t ON t.id = p.tenant_id
	LEFT JOIN users cb ON cb.id = p.created_by
	LEFT JOIN users ju ON ju.id = p.used_by`

func (s *Store) scanPairing(rs rowScanner) (Pairing, error) {
	var (
		p                 Pairing
		user, usedAt, rev *string
		ue                sql.NullInt64
		exp               int64
		caStr             string
		apikey            int
	)
	err := rs.Scan(&p.ID, &p.TenantID, &p.Tenant, &p.Description, &user, &apikey, &ue,
		&p.CreatedBy, &caStr, &exp, &usedAt, &p.UsedBy, &rev)
	if err != nil {
		return Pairing{}, err
	}
	p.Username, p.UsedAt, p.RevokedAt = deref(user), derefTime(usedAt), derefTime(rev)
	p.APIKey = apikey != 0
	if t, err := time.Parse(tsFmt, caStr); err == nil {
		p.CreatedAt = t
	}
	if ue.Valid && ue.Int64 > 0 {
		t := time.Unix(ue.Int64, 0).UTC()
		p.UserExpires = &t
	}
	p.ExpiresAt = time.Unix(exp, 0).UTC()
	switch {
	case p.UsedAt != nil:
		p.Status = "used"
	case p.RevokedAt != nil:
		p.Status = "revoked"
	case p.ExpiresAt.Before(time.Now().UTC()):
		p.Status = "expired"
	default:
		p.Status = "pending"
	}
	return p, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefTime(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse(tsFmt, *s)
	if err != nil {
		return nil
	}
	return &t
}

// ListPairings is the standard scoped list: tenantID 0 (super admin) sees
// every tenant, otherwise only that tenant's invites.
func (s *Store) ListPairings(ctx context.Context, tenantID int64, o query.List) ([]Pairing, int, error) {
	var conds []string
	var args []any
	if tenantID != 0 {
		conds = append(conds, "p.tenant_id = ?")
		args = append(args, tenantID)
	}
	if o.Tenant != "" {
		conds = append(conds, "t.name = ?")
		args = append(args, o.Tenant)
	}
	if o.Q != "" {
		like := o.Like()
		switch o.Col {
		case "description":
			conds = append(conds, `p.description LIKE ? ESCAPE '\'`)
			args = append(args, like)
		case "username":
			conds = append(conds, `p.username LIKE ? ESCAPE '\'`)
			args = append(args, like)
		case "tenant":
			conds = append(conds, `t.name LIKE ? ESCAPE '\'`)
			args = append(args, like)
		default:
			conds = append(conds, `(p.description LIKE ? ESCAPE '\' OR p.username LIKE ? ESCAPE '\' OR t.name LIKE ? ESCAPE '\' OR COALESCE(ju.username, '') LIKE ? ESCAPE '\')`)
			args = append(args, like, like, like, like)
		}
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	total, err := s.count(ctx, "SELECT COUNT(*) "+pairFrom+where, args...)
	if err != nil {
		return nil, 0, err
	}
	order := map[string]string{"created": "p.created_at", "expires": "p.expires_at",
		"description": "p.description", "username": "p.username", "used": "p.used_at",
		"tenant": "t.name"}[o.Sort]
	q := "SELECT " + pairCols + " " + pairFrom + where + " ORDER BY " + order + o.Order() + ", p.id"
	if o.Limit > 0 {
		q += " LIMIT ? OFFSET ?"
		args = append(args, o.Limit, o.Offset)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Pairing{}
	for rows.Next() {
		p, err := s.scanPairing(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// RevokePairing cancels an unused invite. tenantID != 0 confines the
// operation to that tenant (tenant admins), 0 = any (super admin).
// Expired invites can be revoked too — it just records the intent.
func (s *Store) RevokePairing(ctx context.Context, id, tenantID int64) error {
	now := time.Now().UTC().Format(tsFmt)
	q := "UPDATE pairings SET revoked_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL"
	args := []any{now, id}
	if tenantID != 0 {
		q += " AND tenant_id = ?"
		args = append(args, tenantID)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RedeemPair consumes a claim secret (token or short code) and creates the
// account it describes. Returns the new user plus the one-time plaintext
// password and, when the invite asked for one, a fresh API key.
// The invite is claimed with a guarded UPDATE before anything is created,
// so two simultaneous joins can never both succeed.
func (s *Store) RedeemPairing(ctx context.Context, secret string) (Pairing, User, string, string, error) {
	hash := hashKey(strings.TrimSpace(secret))
	row := s.DB.QueryRowContext(ctx,
		"SELECT "+pairCols+" "+pairFrom+" WHERE p.token_hash = ? OR p.code_hash = ?", hash, hash)
	p, err := s.scanPairing(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Pairing{}, User{}, "", "", ErrPairNotFound
	}
	if err != nil {
		return Pairing{}, User{}, "", "", err
	}
	if p.UsedAt != nil {
		return p, User{}, "", "", ErrPairUsed
	}
	if p.RevokedAt != nil {
		return p, User{}, "", "", ErrPairUsed
	}
	if p.ExpiresAt.Before(time.Now().UTC()) {
		return p, User{}, "", "", ErrPairExpired
	}

	now := time.Now().UTC()
	res, err := s.DB.ExecContext(ctx,
		"UPDATE pairings SET used_at = ? WHERE id = ? AND used_at IS NULL", now.Format(tsFmt), p.ID)
	if err != nil {
		return p, User{}, "", "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return p, User{}, "", "", ErrPairUsed // lost the race to another join
	}

	username := p.Username
	if username == "" {
		username = deriveDeviceUsername()
	}
	password, err := randomKey()
	if err != nil {
		s.releaseClaim(ctx, p.ID)
		return p, User{}, "", "", err
	}
	desc := "paired device: " + p.Description
	if len(desc) > 120 {
		desc = desc[:120]
	}
	var u User
	for attempt := 0; ; attempt++ {
		u, err = s.CreateUser(ctx, p.Tenant, username, password, desc, false, false, p.UserExpires)
		if !isUniqueErr(err) || attempt > 5 {
			break
		}
		username = deriveDeviceUsername() // retry only matters for derived-name collisions
	}
	if err != nil {
		s.releaseClaim(ctx, p.ID)
		return p, User{}, "", "", err
	}
	if _, err := s.DB.ExecContext(ctx, "UPDATE pairings SET used_by = ? WHERE id = ?", u.ID, p.ID); err != nil {
		s.releaseClaim(ctx, p.ID)
		return p, User{}, "", "", err
	}
	apikey := ""
	if p.APIKey {
		_, key, err := s.CreateAPIKey(ctx, u.ID, fmt.Sprintf("pairing-%d", p.ID), nil)
		if err != nil {
			s.releaseClaim(ctx, p.ID)
			return p, User{}, "", "", err
		}
		apikey = key
	}
	p.UsedAt = &now
	p.UsedBy = u.Username
	return p, u, password, apikey, nil
}

// releaseClaim puts the invite back on the shelf when account creation
// failed after the claim — the admin's invite can still be redeemed.
func (s *Store) releaseClaim(ctx context.Context, id int64) {
	_, _ = s.DB.ExecContext(ctx, "UPDATE pairings SET used_at = NULL WHERE id = ?", id)
}
