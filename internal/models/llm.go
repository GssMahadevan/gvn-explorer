package models

import (
	"errors"
	"strings"
	"time"
)

// LLM represents a configured large-language-model entry.
type LLM struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Provider      string    `json:"provider"`
	ModelID       string    `json:"model_id"`
	Endpoint      string    `json:"endpoint,omitempty"`
	ContextLength int       `json:"context_length"`
	Enabled       bool      `json:"enabled"`
	Tags          []string  `json:"tags,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Validation errors surfaced by Validate.
var (
	ErrNameRequired     = errors.New("name is required")
	ErrProviderRequired = errors.New("provider is required")
	ErrModelIDRequired  = errors.New("model_id is required")
)

// Validate normalises whitespace and checks required fields.
func (l *LLM) Validate() error {
	l.Name = strings.TrimSpace(l.Name)
	l.Provider = strings.TrimSpace(l.Provider)
	l.ModelID = strings.TrimSpace(l.ModelID)
	l.Endpoint = strings.TrimSpace(l.Endpoint)

	if l.Name == "" {
		return ErrNameRequired
	}
	if l.Provider == "" {
		return ErrProviderRequired
	}
	if l.ModelID == "" {
		return ErrModelIDRequired
	}
	if l.ContextLength < 0 {
		return errors.New("context_length must be >= 0")
	}
	return nil
}

// Patch carries optional fields for partial updates (PATCH).
type Patch struct {
	Name          *string   `json:"name"`
	Provider      *string   `json:"provider"`
	ModelID       *string   `json:"model_id"`
	Endpoint      *string   `json:"endpoint"`
	ContextLength *int      `json:"context_length"`
	Enabled       *bool     `json:"enabled"`
	Tags          *[]string `json:"tags"`
}

// Apply merges the patch into the entry and validates the result.
func (l *LLM) Apply(p Patch) error {
	if p.Name != nil {
		l.Name = *p.Name
	}
	if p.Provider != nil {
		l.Provider = *p.Provider
	}
	if p.ModelID != nil {
		l.ModelID = *p.ModelID
	}
	if p.Endpoint != nil {
		l.Endpoint = *p.Endpoint
	}
	if p.ContextLength != nil {
		l.ContextLength = *p.ContextLength
	}
	if p.Enabled != nil {
		l.Enabled = *p.Enabled
	}
	if p.Tags != nil {
		l.Tags = *p.Tags
	}
	return l.Validate()
}
