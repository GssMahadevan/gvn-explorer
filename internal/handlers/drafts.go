package handlers

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/audit"
)

// Draft/publish endpoints for the config editor. A draft is a server-side
// working copy (internal/drafts) so edits survive reloads and two admins
// share one draft per path — last writer wins. Reading a draft takes the
// r bit; every change to a draft (save, discard, commit) takes the w bit,
// because even *drafting* is a write action on the file. Commit refuses to
// touch the file when the draft is not valid YAML (parse-only,
// multi-document aware) and leaves an fs_commit event in the audit trail.

type draftBody struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// draftPath reads+validates the path of a draft request (query or body).
func draftPath(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	p, err := acl.Normalize(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return "", false
	}
	if p == "/" {
		writeError(w, http.StatusBadRequest, "bad_path", "cannot draft the root")
		return "", false
	}
	if isHelpPath(p) {
		helpReadOnly(w)
		return "", false
	}
	return p, true
}

// handleDraftGet: GET /api/v1/fs/draft?path=/cfg/app.yml — the current
// working copy and its metadata. draft:null means "no draft"; the file
// content itself stays on the /fs/content contract.
func (a *API) handleDraftGet(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if !isHelpPath(p) {
		if !a.check(w, r, p, true, false, false) {
			return
		}
	}
	d, ok, err := a.Drafts.Get(r.Context(), p)
	if err != nil {
		slog.Error("draft get", "path", p, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "draft get failed")
		return
	}
	var anyDraft any
	if ok {
		anyDraft = d
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "draft": anyDraft})
}

// handleDraftPut: PUT /api/v1/fs/draft {"path":..., "content":...} — the
// editor's autosave. Upsert on purpose: repeated saves are the normal
// flow, and concurrent writers intentionally overwrite each other.
func (a *API) handleDraftPut(w http.ResponseWriter, r *http.Request) {
	var b draftBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	p, ok := draftPath(w, r, b.Path)
	if !ok {
		return
	}
	if !a.check(w, r, p, false, true, false) {
		return
	}
	if int64(len(b.Content)) > a.maxBytes() {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "draft exceeds the file size limit")
		return
	}
	u := userFrom(r.Context())
	d, err := a.Drafts.Put(r.Context(), p, b.Content, u.Tenant+"/"+u.Username)
	if err != nil {
		slog.Error("draft put", "path", p, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "draft save failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": d})
}

// handleDraftDelete: DELETE /api/v1/fs/draft?path= — throw the working
// copy away; the file on disk is untouched.
func (a *API) handleDraftDelete(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if isHelpPath(p) {
		helpReadOnly(w)
		return
	}
	if !a.check(w, r, p, false, true, false) {
		return
	}
	if err := a.Drafts.Delete(r.Context(), p); err != nil {
		slog.Error("draft delete", "path", p, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "draft discard failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type commitBody struct {
	Path string `json:"path"`
}

// handleDraftCommit: POST /api/v1/fs/draft/commit {"path":...} — publish.
// Validate, write the real file, drop the draft, audit fs_commit. A
// validation failure leaves both the draft and the file exactly as they
// were: the editor can fix the text and retry.
func (a *API) handleDraftCommit(w http.ResponseWriter, r *http.Request) {
	var b commitBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	p, ok := draftPath(w, r, b.Path)
	if !ok {
		return
	}
	if !a.check(w, r, p, false, true, false) {
		return
	}
	d, found, err := a.Drafts.Get(r.Context(), p)
	if err != nil {
		slog.Error("draft get (commit)", "path", p, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "draft get failed")
		return
	}
	if !found {
		writeError(w, http.StatusConflict, "no_draft", "nothing to publish for this path")
		return
	}
	if err := validateYAML([]byte(d.Content)); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_yaml", err.Error())
		return
	}
	if st, err := a.FS.Stat(p); err == nil && st.IsDir {
		writeError(w, http.StatusConflict, "exists", "path is a directory")
		return
	}
	if err := a.FS.Write(p, []byte(d.Content)); err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	if err := a.Drafts.Delete(r.Context(), p); err != nil {
		// the publish succeeded; a stale draft row must not confuse the
		// next editor, so make noise about it
		slog.Error("draft delete (commit)", "path", p, "err", err)
	}
	u := userFrom(r.Context())
	a.audit(r, audit.Event{Action: "fs_commit", TenantID: u.TenantID,
		TargetType: "file", TargetName: p, Path: p,
		Details: map[string]any{"size": len(d.Content)}})
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "size": len(d.Content)})
}

// validateYAML parses every document in the stream and reports the first
// parse error. Parse-only on purpose: comment-preserving round-trips are
// the client's job; the server just refuses text that is not even YAML.
func validateYAML(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// base_mtime conflict text shared by the error response.
func conflictMsg(base, cur int64) string {
	return "file changed since base_mtime=" + strconv.FormatInt(base, 10) + " (now " + strconv.FormatInt(cur, 10) + ")"
}
