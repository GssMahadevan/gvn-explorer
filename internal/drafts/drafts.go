// Package drafts keeps per-path working copies of file edits — the
// "draft" half of the draft/publish edit model: GUI edits land here and
// the file on disk changes only when a draft is committed. One draft per
// path is the entire concurrency story: two admins editing the same file
// share that single draft, last writer wins.
package drafts

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/user/llm-crud1/internal/db"
)

type Draft struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	UpdatedBy string `json:"updated_by"`
	UpdatedAt int64  `json:"updated_at"`
}

type Store struct{ DB *db.DB }

func NewStore(conn *db.DB) *Store { return &Store{DB: conn} }

// Get returns the working copy for a path. The absence of a draft is the
// normal case, reported as (zero, false, nil).
func (s *Store) Get(ctx context.Context, path string) (Draft, bool, error) {
	var d Draft
	err := s.DB.QueryRowContext(ctx,
		"SELECT path, content, updated_by, updated_at FROM file_drafts WHERE path = ?", path).
		Scan(&d.Path, &d.Content, &d.UpdatedBy, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	return d, true, nil
}

// Put creates or replaces the working copy for a path (last writer wins).
func (s *Store) Put(ctx context.Context, path, content, updatedBy string) (Draft, error) {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO file_drafts (path, content, updated_by, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			content = excluded.content, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		path, content, updatedBy, now)
	if err != nil {
		return Draft{}, err
	}
	return Draft{Path: path, Content: content, UpdatedBy: updatedBy, UpdatedAt: now}, nil
}

// Delete discards a working copy. Discarding nothing is not an error:
// the discard action should be idempotent for the caller.
func (s *Store) Delete(ctx context.Context, path string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM file_drafts WHERE path = ?", path)
	return err
}
