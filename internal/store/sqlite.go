package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/user/llm-crud1/internal/models"
	_ "modernc.org/sqlite"
)

// SQLiteStore is a durable Store backed by SQLite (pure-Go driver, no CGO).
type SQLiteStore struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS llms (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL UNIQUE COLLATE NOCASE,
	provider       TEXT NOT NULL,
	model_id       TEXT NOT NULL,
	endpoint       TEXT NOT NULL DEFAULT '',
	context_length INTEGER NOT NULL DEFAULT 0,
	enabled        INTEGER NOT NULL DEFAULT 1,
	tags           TEXT NOT NULL DEFAULT '[]',
	created_at     TEXT NOT NULL,
	updated_at     TEXT NOT NULL
);
`

// NewSQLiteStore opens (and if needed creates) the database at path.
// Use ":memory:" for an ephemeral database.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

// isDuplicate reports a UNIQUE-constraint violation.
func isDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *SQLiteStore) List(ctx context.Context, f ListFilter) ([]models.LLM, error) {
	var where []string
	var args []any
	if f.Provider != "" {
		where = append(where, "provider LIKE ?")
		args = append(args, f.Provider)
	}
	if f.Enabled != nil {
		where = append(where, "enabled = ?")
		args = append(args, boolInt(*f.Enabled))
	}
	if f.Query != "" {
		where = append(where, "(name LIKE ? OR model_id LIKE ?)")
		q := "%" + f.Query + "%"
		args = append(args, q, q)
	}
	qs := "SELECT " + colList + " FROM llms"
	if len(where) > 0 {
		qs += " WHERE " + strings.Join(where, " AND ")
	}
	qs += " ORDER BY created_at"
	rows, err := s.db.QueryContext(ctx, qs, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (models.LLM, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+colList+" FROM llms WHERE id = ?", id)
	return scanRow(row)
}

func (s *SQLiteStore) Create(ctx context.Context, llm models.LLM) (models.LLM, error) {
	now := time.Now().UTC()
	llm.ID = NewID()
	llm.CreatedAt, llm.UpdatedAt = now, now
	tags, err := json.Marshal(llm.Tags)
	if err != nil {
		return models.LLM{}, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO llms (id,name,provider,model_id,endpoint,context_length,enabled,tags,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		llm.ID, llm.Name, llm.Provider, llm.ModelID, llm.Endpoint,
		llm.ContextLength, boolInt(llm.Enabled), string(tags),
		formatTS(llm.CreatedAt), formatTS(llm.UpdatedAt))
	if err != nil {
		if isDuplicate(err) {
			return models.LLM{}, ErrDuplicate
		}
		return models.LLM{}, err
	}
	return llm, nil
}

func (s *SQLiteStore) Update(ctx context.Context, llm models.LLM) (models.LLM, error) {
	prev, err := s.Get(ctx, llm.ID)
	if err != nil {
		return models.LLM{}, err
	}
	tags, err := json.Marshal(llm.Tags)
	if err != nil {
		return models.LLM{}, err
	}
	llm.CreatedAt = prev.CreatedAt
	llm.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE llms SET name=?,provider=?,model_id=?,endpoint=?,context_length=?,enabled=?,tags=?,updated_at=?
		 WHERE id=?`,
		llm.Name, llm.Provider, llm.ModelID, llm.Endpoint,
		llm.ContextLength, boolInt(llm.Enabled), string(tags),
		formatTS(llm.UpdatedAt), llm.ID)
	if err != nil {
		if isDuplicate(err) {
			return models.LLM{}, ErrDuplicate
		}
		return models.LLM{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.LLM{}, ErrNotFound
	}
	return llm, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM llms WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM llms").Scan(&n)
	return n, err
}

// ---- row mapping helpers ----

const colList = "id,name,provider,model_id,endpoint,context_length,enabled,tags,created_at,updated_at"

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const tsLayout = time.RFC3339Nano

func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(s string) (time.Time, error) { return time.Parse(tsLayout, s) }

func applyRow(id, name, provider, modelID, endpoint string, ctxLen int, en int, tagsJSON, createdAt, updatedAt string) (models.LLM, error) {
	l := models.LLM{
		ID: id, Name: name, Provider: provider, ModelID: modelID,
		Endpoint: endpoint, ContextLength: ctxLen, Enabled: en != 0,
	}
	if tagsJSON != "" && tagsJSON != "[]" {
		if err := json.Unmarshal([]byte(tagsJSON), &l.Tags); err != nil {
			return l, fmt.Errorf("tags: %w", err)
		}
	}
	var err error
	if l.CreatedAt, err = parseTS(createdAt); err != nil {
		return l, fmt.Errorf("created_at: %w", err)
	}
	if l.UpdatedAt, err = parseTS(updatedAt); err != nil {
		return l, fmt.Errorf("updated_at: %w", err)
	}
	return l, nil
}

func scanRow(row *sql.Row) (models.LLM, error) {
	var (
		id, name, provider, modelID, endpoint, tagsJSON, createdAt, updatedAt string
		ctxLen, en                                                            int
	)
	if err := row.Scan(&id, &name, &provider, &modelID, &endpoint, &ctxLen, &en, &tagsJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.LLM{}, ErrNotFound
		}
		return models.LLM{}, err
	}
	return applyRow(id, name, provider, modelID, endpoint, ctxLen, en, tagsJSON, createdAt, updatedAt)
}

func scanRows(rows *sql.Rows) ([]models.LLM, error) {
	out := []models.LLM{}
	for rows.Next() {
		var (
			id, name, provider, modelID, endpoint, tagsJSON, createdAt, updatedAt string
			ctxLen, en                                                            int
		)
		if err := rows.Scan(&id, &name, &provider, &modelID, &endpoint, &ctxLen, &en, &tagsJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		l, err := applyRow(id, name, provider, modelID, endpoint, ctxLen, en, tagsJSON, createdAt, updatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
