// Package clip keeps tenant clipboards: named link feeds through which
// users hand files to each other across devices. A clipboard holds no
// bytes of its own — every item is a pointer to a file, re-checked
// against that file's ACL on every read, so sharing a big video costs
// nothing extra and revoking the file revokes the share. Items expire
// (server-wide TTL) and are purged lazily when a feed is read: no
// cron, no sweeper.
package clip

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/db"
)

// MimeKinds is the explicit allow-list a clipboard declares so devices
// and OS apps know how to treat its content. Base media types only —
// deliberately a small list (same policy as the inline-preview
// allow-lists), not the whole MIME tree. "*/*" means "mixed/unknown".
var MimeKinds = []string{"text/plain", "image/*", "video/*", "audio/*", "application/pdf", "*/*"}

func MimeOK(m string) bool {
	for _, k := range MimeKinds {
		if m == k {
			return true
		}
	}
	return false
}

type Clipboard struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"-"`
	Tenant    string `json:"tenant"`
	Name      string `json:"name"`
	Mime      string `json:"mime"`
	CreatedBy string `json:"created_by"`
	// wire contract mirrors the rest of the API: floored seconds plus
	// the exact millisecond value.
	CreatedAt int64 `json:"created_at"`
	// Items is a live (unexpired) item count, filled by List/ByName.
	Items int `json:"items"`
	// LinkPath is the feed's virtual path in the tree (computed by the
	// handler, never stored).
	LinkPath string `json:"link_path,omitempty"`
}

type Item struct {
	ID          int64  `json:"id"`
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	PostedBy    string `json:"posted_by"`
	PostedAt    int64  `json:"posted_at"`
	ExpiresAt   int64  `json:"expires_at"`
	PostedAtMs  int64  `json:"posted_at_ms"`
	ExpiresAtMs int64  `json:"expires_at_ms"`
	// LinkPath is the item's virtual path in the tree (computed by the
	// handler, never stored).
	LinkPath string `json:"link_path,omitempty"`
}

type Store struct {
	DB db.Execer
	// MaxItems caps live items per feed (-clip-max-items; <=0 = no cap).
	MaxItems int
}

func NewStore(conn *db.DB) *Store { return &Store{DB: conn} }

// WithTx returns a store whose queries run inside tx.
func (s *Store) WithTx(tx *db.Tx) *Store { c := *s; c.DB = tx; return &c }

// Create adds a named clipboard to a tenant. Names are unique per
// tenant; the caller surfaces the constraint as 409.
func (s *Store) Create(ctx context.Context, tenantID int64, name, mime, createdBy string) (Clipboard, error) {
	now := time.Now().UnixMilli()
	res, err := s.DB.ExecContext(ctx,
		"INSERT INTO clipboards (tenant_id, name, mime, created_by, created_at) VALUES (?, ?, ?, ?, ?)",
		tenantID, name, mime, createdBy, now)
	if err != nil {
		return Clipboard{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Clipboard{}, err
	}
	return Clipboard{ID: id, TenantID: tenantID, Name: name, Mime: mime,
		CreatedBy: createdBy, CreatedAt: now / 1000}, nil
}

// ByID / ByName fetch one clipboard with its tenant name and live item
// count; absence is (zero, false, nil).
func (s *Store) ByID(ctx context.Context, id int64) (Clipboard, bool, error) {
	return s.one(ctx, "SELECT c.id, c.tenant_id, t.name, c.name, c.mime, c.created_by, c.created_at, "+
		liveCount+" FROM clipboards c JOIN tenants t ON t.id = c.tenant_id WHERE c.id = ?", id)
}

func (s *Store) ByName(ctx context.Context, tenantID int64, name string) (Clipboard, bool, error) {
	return s.one(ctx, "SELECT c.id, c.tenant_id, t.name, c.name, c.mime, c.created_by, c.created_at, "+
		liveCount+" FROM clipboards c JOIN tenants t ON t.id = c.tenant_id WHERE c.tenant_id = ? AND c.name = ?",
		tenantID, name)
}

const liveCount = "(SELECT COUNT(*) FROM clipboard_items i WHERE i.clipboard_id = c.id AND i.expires_at > ?)"

func (s *Store) one(ctx context.Context, q string, args ...any) (Clipboard, bool, error) {
	var c Clipboard
	// liveCount's placeholder comes first: it sits in the SELECT list.
	now := time.Now().UnixMilli()
	err := s.DB.QueryRowContext(ctx, q, append([]any{now}, args...)...).
		Scan(&c.ID, &c.TenantID, &c.Tenant, &c.Name, &c.Mime, &c.CreatedBy, &c.CreatedAt, &c.Items)
	if errors.Is(err, sql.ErrNoRows) {
		return Clipboard{}, false, nil
	}
	if err != nil {
		return Clipboard{}, false, err
	}
	return c, true, nil
}

// List returns a tenant's clipboards (newest first) — the feed index
// is tenant-wide by design; the per-item read gate is the target
// file's ACL, not membership here.
func (s *Store) List(ctx context.Context, tenantID int64) ([]Clipboard, error) {
	now := time.Now().UnixMilli()
	rows, err := s.DB.QueryContext(ctx,
		"SELECT c.id, c.tenant_id, t.name, c.name, c.mime, c.created_by, c.created_at, "+
			liveCount+" FROM clipboards c JOIN tenants t ON t.id = c.tenant_id WHERE c.tenant_id = ? ORDER BY c.id DESC",
		now, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Clipboard
	for rows.Next() {
		var c Clipboard
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Tenant, &c.Name, &c.Mime, &c.CreatedBy, &c.CreatedAt, &c.Items); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes a clipboard; items cascade with it (schema FK).
func (s *Store) Delete(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM clipboards WHERE id = ?", id)
	return err
}

// AddItem appends a link to a feed. The caller has already checked
// that the target exists and that the poster can read it; the cap
// check happens here so concurrent posters cannot race past it.
func (s *Store) AddItem(ctx context.Context, clipboardID int64, path, contentType, postedBy string, ttl time.Duration) (Item, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM clipboard_items WHERE clipboard_id = ? AND expires_at > ?",
		clipboardID, time.Now().UnixMilli()).Scan(&n); err != nil {
		return Item{}, err
	}
	if s.MaxItems > 0 && n >= s.MaxItems {
		return Item{}, ErrFull
	}
	now := time.Now().UnixMilli()
	res, err := s.DB.ExecContext(ctx,
		"INSERT INTO clipboard_items (clipboard_id, path, content_type, posted_by, posted_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
		clipboardID, path, contentType, postedBy, now, now+ttl.Milliseconds())
	if err != nil {
		return Item{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Item{}, err
	}
	return Item{ID: id, Path: path, ContentType: contentType, PostedBy: postedBy,
		PostedAt: now / 1000, ExpiresAt: (now + ttl.Milliseconds()) / 1000,
		PostedAtMs: now, ExpiresAtMs: now + ttl.Milliseconds()}, nil
}

// ErrFull: the feed holds its cap of live items.
var ErrFull = errors.New("clipboard is full")

// Items returns a feed's live items, newest first, purging expired
// rows on the way (the lazy purge: reads are the only sweeper).
func (s *Store) Items(ctx context.Context, clipboardID int64) ([]Item, error) {
	now := time.Now().UnixMilli()
	if _, err := s.DB.ExecContext(ctx,
		"DELETE FROM clipboard_items WHERE clipboard_id = ? AND expires_at <= ?", clipboardID, now); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT id, path, content_type, posted_by, posted_at, expires_at FROM clipboard_items WHERE clipboard_id = ? ORDER BY id DESC",
		clipboardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Path, &it.ContentType, &it.PostedBy, &it.PostedAtMs, &it.ExpiresAtMs); err != nil {
			return nil, err
		}
		it.PostedAt = it.PostedAtMs / 1000
		it.ExpiresAt = it.ExpiresAtMs / 1000
		out = append(out, it)
	}
	return out, rows.Err()
}

// ItemByID fetches one item of a feed by id (the id parsed back out of
// a virtual-tree entry name).
func (s *Store) ItemByID(ctx context.Context, clipboardID, id int64) (Item, bool, error) {
	var it Item
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, path, content_type, posted_by, posted_at, expires_at FROM clipboard_items WHERE clipboard_id = ? AND id = ?",
		clipboardID, id).Scan(&it.ID, &it.Path, &it.ContentType, &it.PostedBy, &it.PostedAtMs, &it.ExpiresAtMs)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	it.PostedAt = it.PostedAtMs / 1000
	it.ExpiresAt = it.ExpiresAtMs / 1000
	return it, true, nil
}

// DeleteItemsForPath drops links to a path that just left the tree
// (deleted or renamed away). Not called by v1 handlers — the dead
// flag makes stale links honest — but it is the hook if operators
// ever want eager cleanup.
func (s *Store) DeleteItemsForPath(ctx context.Context, path string) (int, error) {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM clipboard_items WHERE path = ?", path)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
