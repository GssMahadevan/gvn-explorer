package handlers

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/user/llm-crud1/internal/acl"
)

func (a *API) maxBytes() int64 {
	mb := int64(a.MaxFileMB)
	if mb <= 0 {
		mb = 10
	}
	return mb << 20
}

// handleList: GET /api/v1/fs?path=/dir
func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if !a.check(w, r, p, true, false, false) {
		return
	}
	entries, err := a.FS.List(p)
	if err != nil {
		if !mapFsErr(w, err) {
			slog.Error("list", "path", p, "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "list failed")
		}
		return
	}
	// Hide entries the caller cannot read.
	u := userFrom(r.Context())
	visible := make([]any, 0, len(entries))
	for _, e := range entries {
		bits, err := a.ACL.Effective(r.Context(), a.Auth, e.Path, u)
		if err != nil {
			continue
		}
		if !bits.Read {
			continue
		}
		visible = append(visible, struct {
			Entry any      `json:"entry"`
			Perms acl.Bits `json:"perms"`
		}{Entry: e, Perms: bits})
	}
	self, _ := a.ACL.Effective(r.Context(), a.Auth, p, u)
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "perms": self, "entries": visible})
}

// handleContent: GET /api/v1/fs/content?path=/f.txt — JSON text view.
func (a *API) handleContent(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if !a.check(w, r, p, true, false, false) {
		return
	}
	st, err := a.FS.Stat(p)
	if err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", "stat failed")
		}
		return
	}
	if st.IsDir {
		writeError(w, http.StatusBadRequest, "is_a_dir", "use /api/v1/fs to list directories")
		return
	}
	if st.Size > a.maxBytes() {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "file exceeds limit; download via /api/v1/fs/raw")
		return
	}
	data, err := a.FS.ReadText(p)
	if err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", "read failed")
		}
		return
	}
	if bytes.IndexByte(data, 0) >= 0 {
		writeJSON(w, http.StatusOK, map[string]any{"path": p, "size": st.Size, "is_binary": true, "content": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "size": st.Size, "is_binary": false, "content": string(data)})
}

// handleRaw: GET /api/v1/fs/raw?path=/f.bin — stream bytes for download.
func (a *API) handleRaw(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if !a.check(w, r, p, true, false, false) {
		return
	}
	abs, err := a.FS.Abs(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+strconvQuote(path.Base(p)))
	http.ServeFile(w, r, abs)
}

type pathBody struct {
	Path string `json:"path"`
}

// handleMkdir: POST /api/v1/fs/mkdir {"path":"/dir/new"}
func (a *API) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var b pathBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	p, err := acl.Normalize(b.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if p == "/" {
		writeError(w, http.StatusBadRequest, "bad_path", "cannot mkdir root")
		return
	}
	if !a.check(w, r, p, false, true, false) {
		return
	}
	if err := a.FS.Mkdir(p); err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": p})
}

type writeBody struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// handleWrite: POST /api/v1/fs/write {"path":"/f.txt","content":"..."}
func (a *API) handleWrite(w http.ResponseWriter, r *http.Request) {
	var b writeBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	p, err := acl.Normalize(b.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if p == "/" {
		writeError(w, http.StatusBadRequest, "bad_path", "cannot write root as file")
		return
	}
	if !a.check(w, r, p, false, true, false) {
		return
	}
	if st, err := a.FS.Stat(p); err == nil && st.IsDir {
		writeError(w, http.StatusConflict, "exists", "path is a directory")
		return
	}
	if err := a.FS.Write(p, []byte(b.Content)); err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "size": len(b.Content)})
}

type renameBody struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// handleRename: POST /api/v1/fs/rename {"from":"/a","to":"/b"}
func (a *API) handleRename(w http.ResponseWriter, r *http.Request) {
	var b renameBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	from, err := acl.Normalize(b.From)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	to, err := acl.Normalize(b.To)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if from == "/" || to == "/" {
		writeError(w, http.StatusBadRequest, "bad_path", "cannot rename root")
		return
	}
	if !a.check(w, r, from, false, false, true) || !a.check(w, r, to, false, true, false) {
		return
	}
	if err := a.FS.Rename(from, to); err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to})
}

// handleDelete: DELETE /api/v1/fs?path=/f.txt
func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	p, err := logicalPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if p == "/" {
		writeError(w, http.StatusBadRequest, "bad_path", "cannot delete root")
		return
	}
	if !a.check(w, r, p, false, false, true) {
		return
	}
	if err := a.FS.Delete(p); err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUpload: POST /api/v1/fs/upload multipart form: path=<dir>, file=<f>
func (a *API) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxBytes())
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "multipart form required (path=<dir>, file=<f>)")
		return
	}
	dir, err := acl.Normalize(r.FormValue("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "file part required")
		return
	}
	defer file.Close()
	name := path.Base(strings.ReplaceAll(hdr.Filename, "\\", "/"))
	if name == "." || name == "/" || name == ".." {
		writeError(w, http.StatusBadRequest, "bad_path", "invalid filename")
		return
	}
	target := dir
	if target == "/" {
		target = "/" + name
	} else {
		target = dir + "/" + name
	}
	if !a.check(w, r, target, false, true, false) {
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "failed to read upload")
		return
	}
	if err := a.FS.Write(target, data); err != nil {
		if !mapFsErr(w, err) {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": target, "size": len(data)})
}

// strconvQuote guards against header-injection characters in filenames.
func strconvQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "", "\r", "").Replace(s) + `"`
}
