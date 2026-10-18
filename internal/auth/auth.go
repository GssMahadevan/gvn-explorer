// Package auth: users, groups, memberships, bcrypt passwords, cookie sessions.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var ErrNotFound = errors.New("not found")

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

type Group struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Store struct{ DB *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

const tsFmt = time.RFC3339Nano

// CreateUser adds the user and returns it. admin grants full bypass rights.
func (s *Store) CreateUser(ctx context.Context, username, password string, admin bool) (User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	var adminInt int
	if admin {
		adminInt = 1
	}
	res, err := s.DB.ExecContext(ctx,
		"INSERT INTO users (username, pass_hash, is_admin, created_at) VALUES (?,?,?,?)",
		username, string(hash), adminInt, time.Now().UTC().Format(tsFmt))
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, IsAdmin: admin}, nil
}

// Verify checks credentials and returns the user, or ErrNotFound on mismatch.
func (s *Store) Verify(ctx context.Context, username, password string) (User, error) {
	var (
		u     User
		hash  string
		ad    int
		caStr string
	)
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, username, pass_hash, is_admin, created_at FROM users WHERE username = ?", username).
		Scan(&u.ID, &u.Username, &hash, &ad, &caStr)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, ErrNotFound
	}
	u.IsAdmin = ad != 0
	u.CreatedAt, _ = time.Parse(tsFmt, caStr)
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	var (
		u     User
		ad    int
		caStr string
	)
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, username, is_admin, created_at FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Username, &ad, &caStr)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u.IsAdmin = ad != 0
	u.CreatedAt, _ = time.Parse(tsFmt, caStr)
	return u, err
}

func (s *Store) UserByName(ctx context.Context, username string) (User, error) {
	var (
		id    int64
		ad    int
		caStr string
	)
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, is_admin, created_at FROM users WHERE username = ?", username).Scan(&id, &ad, &caStr)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	u := User{ID: id, Username: username, IsAdmin: ad != 0}
	u.CreatedAt, _ = time.Parse(tsFmt, caStr)
	return u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, username, is_admin, created_at FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var (
			u     User
			ad    int
			caStr string
		)
		if err := rows.Scan(&u.ID, &u.Username, &ad, &caStr); err != nil {
			return nil, err
		}
		u.IsAdmin = ad != 0
		u.CreatedAt, _ = time.Parse(tsFmt, caStr)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetPassword(ctx context.Context, id int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE users SET pass_hash = ? WHERE id = ?", string(hash), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- groups ----

func (s *Store) CreateGroup(ctx context.Context, name string) (Group, error) {
	res, err := s.DB.ExecContext(ctx, "INSERT INTO groups (name) VALUES (?)", name)
	if err != nil {
		return Group{}, err
	}
	id, _ := res.LastInsertId()
	return Group{ID: id, Name: name}, nil
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id, name FROM groups ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GroupByName(ctx context.Context, name string) (Group, error) {
	var g Group
	err := s.DB.QueryRowContext(ctx, "SELECT id, name FROM groups WHERE name = ?", name).Scan(&g.ID, &g.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	return g, err
}

func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM groups WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddMember(ctx context.Context, groupID, userID int64) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO group_members (group_id, user_id) VALUES (?,?)", groupID, userID)
	return err
}

func (s *Store) RemoveMember(ctx context.Context, groupID, userID int64) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM group_members WHERE group_id=? AND user_id=?", groupID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GroupIDsFor lists the groups a user belongs to (for ACL resolution).
func (s *Store) GroupIDsFor(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT group_id FROM group_members WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- sessions ----

func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateSession issues a session token for the user, valid for ttl.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = s.DB.ExecContext(ctx,
		"INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?,?,?,?)",
		id, userID, now.Format(tsFmt), now.Add(ttl).Format(tsFmt))
	return id, err
}

// SessionUser resolves a session token to its user; expired/unknown -> ErrNotFound.
func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	var (
		userID int64
		expStr string
	)
	err := s.DB.QueryRowContext(ctx,
		"SELECT user_id, expires_at FROM sessions WHERE id = ?", token).Scan(&userID, &expStr)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	exp, err := time.Parse(tsFmt, expStr)
	if err != nil || time.Now().UTC().After(exp) {
		s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", token)
		return User{}, ErrNotFound
	}
	return s.UserByID(ctx, userID)
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", token)
	return err
}
