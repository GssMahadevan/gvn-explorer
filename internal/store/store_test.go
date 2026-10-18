package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/user/llm-crud1/internal/models"
)

// newStores returns every Store implementation under a common name.
func newStores(t *testing.T) map[string]Store {
	t.Helper()
	mem := map[string]Store{"memory": NewMemoryStore()}
	db := filepath.Join(t.TempDir(), "test.db")
	sqlite, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	sqlite.db.SetMaxOpenConns(1)
	mem["sqlite"] = sqlite
	return mem
}

func mkLLM(name string) models.LLM {
	return models.LLM{Name: name, Provider: "openai", ModelID: "gpt-4o-mini", ContextLength: 128000, Enabled: true, Tags: []string{"t"}}
}

func TestCRUDLifecycle(t *testing.T) {
	for name, s := range newStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			created, err := s.Create(ctx, mkLLM("alpha"))
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if created.ID == "" || created.CreatedAt.IsZero() {
				t.Fatalf("create did not set ID/CreatedAt: %+v", created)
			}

			// Duplicate name, different case -> ErrDuplicate.
			if _, err := s.Create(ctx, mkLLM("ALPHA")); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate: want ErrDuplicate, got %v", err)
			}

			got, err := s.Get(ctx, created.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got.Name != "alpha" || len(got.Tags) != 1 {
				t.Fatalf("get round-trip mismatch: %+v", got)
			}

			got.Endpoint = "http://example.test"
			got.Enabled = false
			updated, err := s.Update(ctx, got)
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if !updated.UpdatedAt.After(updated.CreatedAt) {
				t.Errorf("UpdatedAt should advance: created=%v updated=%v", updated.CreatedAt, updated.UpdatedAt)
			}
			again, err := s.Get(ctx, created.ID)
			if err != nil || again.Endpoint != "http://example.test" || again.Enabled {
				t.Fatalf("update not persisted: %+v err=%v", again, err)
			}

			if err := s.Delete(ctx, created.ID); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("get deleted: want ErrNotFound, got %v", err)
			}
			if err := s.Delete(ctx, created.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("double delete: want ErrNotFound, got %v", err)
			}
		})
	}
}

func TestListFilters(t *testing.T) {
	for name, s := range newStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			seeds := []models.LLM{
				{Name: "one", Provider: "openai", ModelID: "gpt-4o", Enabled: true},
				{Name: "two", Provider: "ollama", ModelID: "llama3:8b", Enabled: false},
				{Name: "three", Provider: "Anthropic", ModelID: "claude-3-5-sonnet", Enabled: true},
			}
			for _, l := range seeds {
				if _, err := s.Create(ctx, l); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}

			all, err := s.List(ctx, ListFilter{})
			if err != nil || len(all) != 3 {
				t.Fatalf("list all: got %d err=%v", len(all), err)
			}

			byProv, err := s.List(ctx, ListFilter{Provider: "OLLAMA"})
			if err != nil || len(byProv) != 1 || byProv[0].Name != "two" {
				t.Fatalf("provider filter (case-insensitive): %+v err=%v", byProv, err)
			}

			yes := true
			byEnabled, err := s.List(ctx, ListFilter{Enabled: &yes})
			if err != nil || len(byEnabled) != 2 {
				t.Fatalf("enabled filter: got %d err=%v", len(byEnabled), err)
			}

			byQ, err := s.List(ctx, ListFilter{Query: "claude"})
			if err != nil || len(byQ) != 1 || byQ[0].Name != "three" {
				t.Fatalf("query filter: %+v err=%v", byQ, err)
			}

			n, err := s.Count(ctx)
			if err != nil || n != 3 {
				t.Fatalf("count: got %d err=%v", n, err)
			}
		})
	}
}
