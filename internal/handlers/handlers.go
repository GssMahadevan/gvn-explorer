package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/user/llm-crud1/internal/models"
	"github.com/user/llm-crud1/internal/store"
)

// API bundles dependencies and registers routes on a mux.
type API struct {
	Store store.Store
	Log   *slog.Logger
}

// Register mounts all routes onto mux using Go 1.22 method+wildcard patterns.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /api/v1/llms", a.handleList)
	mux.HandleFunc("POST /api/v1/llms", a.handleCreate)
	mux.HandleFunc("GET /api/v1/llms/{id}", a.handleGet)
	mux.HandleFunc("PUT /api/v1/llms/{id}", a.handlePut)
	mux.HandleFunc("PATCH /api/v1/llms/{id}", a.handlePatch)
	mux.HandleFunc("DELETE /api/v1/llms/{id}", a.handleDelete)
}

// ---- helpers ----

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			slog.Error("encode response", "err", err)
		}
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var e errorBody
	e.Error.Code = code
	e.Error.Message = msg
	writeJSON(w, status, e)
}

// mapStoreErr translates store sentinel errors into HTTP responses.
func mapStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicate", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", "unexpected error")
	}
}

// decodeBody reads a JSON object into dst, rejecting unknown fields.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tj *json.UnmarshalTypeError
		switch {
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, "bad_request", "request body is required")
		case errors.As(err, &tj):
			writeError(w, http.StatusBadRequest, "bad_request", "invalid field types")
		case strings.Contains(err.Error(), "unknown field"):
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		}
		return false
	}
	return true
}

// ---- handlers ----

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	n, _ := a.Store.Count(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"count":  n,
	})
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ListFilter{
		Provider: q.Get("provider"),
		Query:    q.Get("q"),
	}
	if s := q.Get("enabled"); s != "" {
		b := s == "true" || s == "1"
		f.Enabled = &b
	}
	items, err := a.Store.List(r.Context(), f)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	it, err := a.Store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	var llm models.LLM
	if !decodeBody(w, r, &llm) {
		return
	}
	if err := llm.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	created, err := a.Store.Create(r.Context(), llm)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) handlePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var llm models.LLM
	if !decodeBody(w, r, &llm) {
		return
	}
	llm.ID = id
	if err := llm.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	updated, err := a.Store.Update(r.Context(), llm)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *API) handlePatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p models.Patch
	if !decodeBody(w, r, &p) {
		return
	}
	prev, err := a.Store.Get(r.Context(), id)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	if err := prev.Apply(p); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	updated, err := a.Store.Update(r.Context(), prev)
	if err != nil {
		mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Delete(r.Context(), r.PathValue("id")); err != nil {
		mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
