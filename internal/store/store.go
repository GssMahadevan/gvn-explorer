package store

import (
	"context"
	"errors"

	"github.com/user/llm-crud1/internal/models"
)

// Sentinel errors returned by Store implementations.
var (
	ErrNotFound  = errors.New("llm not found")
	ErrDuplicate = errors.New("llm with this name already exists")
)

// ListFilter narrows List results; zero values mean "no filter".
type ListFilter struct {
	Provider string
	Enabled  *bool
	Query    string // case-insensitive substring match on name/model_id
}

// Store abstracts persistence for LLM entries.
type Store interface {
	List(ctx context.Context, f ListFilter) ([]models.LLM, error)
	Get(ctx context.Context, id string) (models.LLM, error)
	Create(ctx context.Context, llm models.LLM) (models.LLM, error)
	Update(ctx context.Context, llm models.LLM) (models.LLM, error)
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
}
