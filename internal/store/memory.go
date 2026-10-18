package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/user/llm-crud1/internal/models"
)

// MemoryStore is a thread-safe in-memory Store. It will be swapped for a
// database-backed implementation once the API surface is stable.
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]models.LLM
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]models.LLM)}
}

// NewID returns a random 16-char hex identifier.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail; fall back to time-based suffix.
		return hex.EncodeToString([]byte(time.Now().String()))[:16]
	}
	return hex.EncodeToString(b)
}

func (s *MemoryStore) List(_ context.Context, f ListFilter) ([]models.LLM, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]models.LLM, 0, len(s.items))
	for _, it := range s.items {
		if f.Provider != "" && !strings.EqualFold(it.Provider, f.Provider) {
			continue
		}
		if f.Enabled != nil && it.Enabled != *f.Enabled {
			continue
		}
		if f.Query != "" {
			q := strings.ToLower(f.Query)
			if !strings.Contains(strings.ToLower(it.Name), q) &&
				!strings.Contains(strings.ToLower(it.ModelID), q) {
				continue
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (models.LLM, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	it, ok := s.items[id]
	if !ok {
		return models.LLM{}, ErrNotFound
	}
	return it, nil
}

func (s *MemoryStore) Create(_ context.Context, llm models.LLM) (models.LLM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.nameTakenLocked(llm.Name, "") {
		return models.LLM{}, ErrDuplicate
	}
	now := time.Now().UTC()
	llm.ID = NewID()
	llm.CreatedAt = now
	llm.UpdatedAt = now
	s.items[llm.ID] = llm
	return llm, nil
}

func (s *MemoryStore) Update(_ context.Context, llm models.LLM) (models.LLM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev, ok := s.items[llm.ID]
	if !ok {
		return models.LLM{}, ErrNotFound
	}
	if s.nameTakenLocked(llm.Name, llm.ID) {
		return models.LLM{}, ErrDuplicate
	}
	llm.CreatedAt = prev.CreatedAt
	llm.UpdatedAt = time.Now().UTC()
	s.items[llm.ID] = llm
	return llm, nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[id]; !ok {
		return ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func (s *MemoryStore) Count(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items), nil
}

// nameTakenLocked reports whether name is used by another entry (case-insensitive).
// Callers must hold the write lock. excludeID skips the entry being updated.
func (s *MemoryStore) nameTakenLocked(name, excludeID string) bool {
	for id, it := range s.items {
		if id != excludeID && strings.EqualFold(it.Name, name) {
			return true
		}
	}
	return false
}
