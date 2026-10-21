package handlers

// Clipboards: cross-device link feeds (see internal/clip for storage).
// Two faces of the same data:
//
//  1. A REST surface for devices and scripts: GET/POST/DELETE
//     /api/v1/clipboards and item add/list under it.
//
//  2. A virtual read-only subtree inside every tenant home —
//     /tenants/{T}/clipboards/{feed}/{id}-{name} — rendered from the
//     DB the same way the embedded /help tree is rendered from the
//     binary. The GUI browses and previews feeds with the normal file
//     endpoints (content/raw resolve through to the target file), and
//     the Perms panel works on the virtual paths because grants are
//     plain path rows. Writes to the subtree go through the clipboard
//     API, never the fs API: the links are owned by the feeds.
//
// Access rules (design, agreed):
//   - creating a feed and posting to one: any tenant member; posting
//     additionally requires READ on the target at post time;
//   - opening an item: gated by the TARGET FILE's ACL, re-checked on
//     every read — a share can never outlive the file's permissions;
//   - a link whose target vanished stays listed, flagged dead;
//   - new feeds carry a default tenant-wide read grant on their
//     virtual path so the Perms panel starts from the truth.

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/acl"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
	"github.com/GssMahadevan/gvn-explorer/internal/clip"
	"github.com/GssMahadevan/gvn-explorer/internal/fsx"
	"github.com/GssMahadevan/gvn-explorer/internal/query"
)

const clipboardsDirName = "clipboards"

// clipReadOnly is the stable rejection every mutating fs call gets
// inside the virtual feed tree (same stance as helpReadOnly).
func clipReadOnly(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "read_only",
		"the clipboards tree is read-only; manage feeds via /api/v1/clipboards")
}

// clipNameRe is the shape a feed name must have: it appears as a
// directory name, so it follows the same character policy as home
// names (see auth.HomeSafe) — but "help"/"tenants" are not reserved
// here, they are only reserved at the root/namespace level.
var clipNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// clipboardsSub maps a logical path into the virtual feed tree:
//
//	/tenants/t1/clipboards           -> ("t1", [], true)
//	/tenants/t1/clipboards/work      -> ("t1", ["work"], true)
//	/tenants/t1/clipboards/work/7-a -> ("t1", ["work","7-a"], true)
//
// Anything else — including a tenant home itself (/tenants/t1) and any
// path not under the home namespace — is (nil, false).
func clipboardsSub(p string) (tenant string, rest []string, ok bool) {
	if !strings.HasPrefix(p, auth.HomeRoot+"/") {
		return "", nil, false
	}
	parts := strings.Split(strings.TrimPrefix(p, auth.HomeRoot+"/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != clipboardsDirName {
		return "", nil, false
	}
	for _, s := range parts {
		if s == "" {
			return "", nil, false // defensive; Normalize keeps paths clean
		}
	}
	return parts[0], parts[2:], true
}

// tenantHomeOf reports the tenant name whose home directory p is
// exactly (/tenants/{T}); that is where the virtual clipboards entry
// is injected into a disk listing.
func tenantHomeOf(p string) (string, bool) {
	if !strings.HasPrefix(p, auth.HomeRoot+"/") {
		return "", false
	}
	rest := strings.TrimPrefix(p, auth.HomeRoot+"/")
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

// entryName is how an item appears in the virtual tree: a numeric id
// prefix (unique, and the only part ever parsed back) plus the
// target's base name so humans recognize the link.
func entryName(id int64, target string) string {
	return strconv.FormatInt(id, 10) + "-" + path.Base(target)
}

// parseEntryName recovers the item id from a tree entry name.
func parseEntryName(name string) (int64, bool) {
	i := strings.IndexByte(name, '-')
	if i <= 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(name[:i], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// ---- REST: feeds ----

// handleClipsList: GET /api/v1/clipboards — the caller's tenant's
// feeds, newest first (entity-list convention: top-level array).
func (a *API) handleClipsList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	clips, err := a.Clips.List(r.Context(), u.TenantID)
	if err != nil {
		slog.Error("clipboards list", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	if clips == nil {
		clips = []clip.Clipboard{}
	}
	writeJSON(w, http.StatusOK, clips)
}

type clipCreateBody struct {
	Name string `json:"name"`
	Mime string `json:"mime"` // optional; "" = "*/*" (mixed feed)
}

// handleClipsCreate: POST /api/v1/clipboards {"name","mime"} — anyone
// in the tenant may create a feed (the agreed rule). The default
// tenant read grant on the feed's virtual path is written in the same
// transaction as the row, so a feed never exists ungrantable.
func (a *API) handleClipsCreate(w http.ResponseWriter, r *http.Request) {
	var b clipCreateBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	u := userFrom(r.Context())
	if !clipNameRe.MatchString(b.Name) {
		writeError(w, http.StatusUnprocessableEntity, "bad_name",
			"feed name must match [a-z0-9][a-z0-9-]{0,31}")
		return
	}
	if b.Mime == "" {
		b.Mime = "*/*"
	}
	if !clip.MimeOK(b.Mime) {
		writeError(w, http.StatusUnprocessableEntity, "bad_mime",
			"mime must be one of: "+strings.Join(clip.MimeKinds, ", "))
		return
	}
	if _, found, err := a.Clips.ByName(r.Context(), u.TenantID, b.Name); err != nil {
		slog.Error("clipboards create lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "create failed")
		return
	} else if found {
		writeError(w, http.StatusConflict, "exists", "this tenant already has a clipboard named "+b.Name)
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defer tx.Rollback()
	cs := a.Clips.WithTx(tx)
	c, err := cs.Create(r.Context(), u.TenantID, b.Name, b.Mime, u.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	home, err := a.homeReadyPath(r.Context(), u.Tenant)
	if err != nil { // no home root: the grant path would not resolve
		writeError(w, http.StatusConflict, "no_home", "tenant has no home directory")
		return
	}
	if err := a.ACL.WithTx(tx).Ensure(r.Context(), home+"/"+clipboardsDirName+"/"+c.Name, "tenant", c.TenantID, true, false, false); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	c.LinkPath = home + "/" + clipboardsDirName + "/" + c.Name
	writeJSON(w, http.StatusCreated, c)
}

type clipPathBody struct {
	Path string `json:"path"`
}

// clipByID loads a feed and checks the caller may work with it:
// same-tenant members (and supers); mismatch is 404 for foreign
// tenants' feeds — other tenants' clipboards are not even nameable to
// the caller. Returns (c, false) after writing the error.
func (a *API) clipByID(w http.ResponseWriter, r *http.Request, id int64) (clip.Clipboard, string, bool) {
	c, found, err := a.Clips.ByID(r.Context(), id)
	if err != nil {
		slog.Error("clipboard lookup", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return c, "", false
	}
	u := userFrom(r.Context())
	if !found || (!u.IsSuper && c.TenantID != u.TenantID) {
		writeError(w, http.StatusNotFound, "not_found", "clipboard not found")
		return c, "", false
	}
	home, err := a.homeReadyPath(r.Context(), c.Tenant)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "clipboard not found")
		return c, "", false
	}
	return c, home, true
}

// handleClipsDelete: DELETE /api/v1/clipboards/{id} — the creator or a
// tenant admin. The feed's grant rows go with it (they exist only to
// guard the virtual path).
func (a *API) handleClipsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "clipboard not found")
		return
	}
	c, home, ok := a.clipByID(w, r, id)
	if !ok {
		return
	}
	u := userFrom(r.Context())
	if c.CreatedBy != u.Username && !(u.IsAdmin || u.IsSuper) {
		writeError(w, http.StatusForbidden, "forbidden", "only the creator or a tenant admin may delete a clipboard")
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defer tx.Rollback()
	grants, err := a.ACL.WithTx(tx).ListAllByPath(r.Context(), home+"/"+clipboardsDirName+"/"+c.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	for _, g := range grants {
		if err := a.ACL.WithTx(tx).Delete(r.Context(), g.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}
	if err := a.Clips.WithTx(tx).Delete(r.Context(), c.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleClipItemAdd: PUT /api/v1/clipboards/{id}/items {"path":...} —
// append a link. Requires the poster's READ on the target right now
// (a share you cannot read would be a permission leak) and a live
// target. The cap (-clip-max-items) is enforced in the store.
func (a *API) handleClipItemAdd(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "clipboard not found")
		return
	}
	var b clipPathBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	c, home, ok := a.clipByID(w, r, id)
	if !ok {
		return
	}
	p, err := acl.Normalize(b.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err.Error())
		return
	}
	if _, _, virt := clipboardsSub(p); virt {
		writeError(w, http.StatusBadRequest, "bad_path", "a link target must be a real file, not a clipboard entry")
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
		writeError(w, http.StatusBadRequest, "is_a_dir", "a link target must be a file")
		return
	}
	ct := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
	if ct == "" {
		ct = "application/octet-stream"
	}
	it, err := a.Clips.AddItem(r.Context(), c.ID, p, ct, userFrom(r.Context()).Username, a.ClipTTL)
	if err != nil {
		if errors.Is(err, clip.ErrFull) {
			writeError(w, http.StatusConflict, "full", "this clipboard holds its maximum of "+strconv.Itoa(a.Clips.MaxItems)+" live items")
			return
		}
		slog.Error("clipboard item add", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "post failed")
		return
	}
	it.LinkPath = home + "/" + clipboardsDirName + "/" + c.Name + "/" + entryName(it.ID, it.Path)
	it.ContentType = ct
	writeJSON(w, http.StatusCreated, it)
}

type itemView struct {
	clip.Item
	LinkPath string `json:"link_path"`
	Dead     bool   `json:"dead"`
	// Readable is the caller's current read bit on the target; live
	// links without it are filtered out of every listing.
	Readable  bool  `json:"readable"`
	Size      int64 `json:"size"`
	ModTimeMs int64 `json:"mod_time_ms"`
}

// handleClipItems: GET /api/v1/clipboards/{id}/items — live items
// (expired rows are purged on the read), newest first; links whose
// targets the caller cannot read are not shown, dead ones are.
func (a *API) handleClipItems(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "clipboard not found")
		return
	}
	c, home, ok := a.clipByID(w, r, id)
	if !ok {
		return
	}
	items, err := a.Clips.Items(r.Context(), c.ID)
	if err != nil {
		slog.Error("clipboard items", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	out := make([]itemView, 0, len(items))
	for _, it := range items {
		out = append(out, a.annotateItem(r, c, home, it))
	}
	// drop unreadable-live links (see handleClipItems doc above)
	keep := out[:0]
	for _, v := range out {
		if v.Dead || v.Readable {
			keep = append(keep, v)
		}
	}
	writeJSON(w, http.StatusOK, keep)
}

// annotateItem resolves one link against the current tree and the
// caller's ACL: dead flag, target size/mtime, and the Readable gate.
func (a *API) annotateItem(r *http.Request, c clip.Clipboard, home string, it clip.Item) itemView {
	v := itemView{Item: it, LinkPath: home + "/" + clipboardsDirName + "/" + c.Name + "/" + entryName(it.ID, it.Path)}
	st, err := a.FS.Stat(it.Path)
	if err != nil {
		v.Dead = true
		return v
	}
	v.Size, v.ModTimeMs = st.Size, st.ModTime.UnixMilli()
	bits, err := a.ACL.Effective(r.Context(), a.Auth, it.Path, userFrom(r.Context()))
	v.Readable = err == nil && bits.Read
	return v
}

// ---- the virtual tree (fs endpoints) ----

// listClipboards answers GET /api/v1/fs for a path inside the virtual
// feed tree. The caller must be a member of the owning tenant (or a
// super); feed entries show the caller's effective bits on each
// feed's virtual path, item entries only for targets the caller can
// read (dead links stay, with zero bits).
func (a *API) listClipboards(w http.ResponseWriter, r *http.Request, p, tn string, rest []string, o query.List) {
	tenant, err := a.Auth.TenantByName(r.Context(), tn)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
		return
	}
	u := userFrom(r.Context())
	if !u.IsSuper && u.TenantID != tenant.ID {
		writeError(w, http.StatusForbidden, "forbidden", "clipboards belong to their tenant")
		return
	}
	home := auth.HomePath(tn)
	self, _ := a.ACL.Effective(r.Context(), a.Auth, p, u)

	switch len(rest) {
	case 0: // the feed index: /tenants/{T}/clipboards
		clips, err := a.Clips.List(r.Context(), tenant.ID)
		if err != nil {
			slog.Error("clipboards list (fs)", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "list failed")
			return
		}
		visible := make([]entryView, 0, len(clips))
		for _, c := range clips {
			bits, err := a.ACL.Effective(r.Context(), a.Auth, home+"/"+clipboardsDirName+"/"+c.Name, u)
			if err != nil || !bits.Read {
				continue
			}
			visible = append(visible, entryView{
				Entry: fsx.Entry{Name: c.Name, Path: home + "/" + clipboardsDirName + "/" + c.Name,
					IsDir: true, ModTime: time.UnixMilli(0)},
				Perms: bits,
			})
		}
		a.finishList(w, r, p, self, visible, o)
	case 1: // one feed: /tenants/{T}/clipboards/{name}
		c, found, err := a.Clips.ByName(r.Context(), tenant.ID, rest[0])
		if !found {
			if err != nil {
				slog.Error("clipboard lookup (fs)", "err", err)
			}
			writeError(w, http.StatusNotFound, "not_found", "path does not exist")
			return
		}
		items, err := a.Clips.Items(r.Context(), c.ID)
		if err != nil {
			slog.Error("clipboard items (fs)", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "list failed")
			return
		}
		visible := make([]entryView, 0, len(items))
		for _, it := range items {
			v := a.annotateItem(r, c, home, it)
			if !v.Dead && !v.Readable {
				continue // unreadable live links are hidden, as on disk
			}
			perms := acl.Bits{}
			if v.Readable {
				perms.Read = true // link-level read only; the target's own write bits do not follow the link
			}
			visible = append(visible, entryView{
				Entry: fsx.Entry{Name: entryName(it.ID, it.Path), Path: v.LinkPath,
					IsDir: false, Size: v.Size, ModTime: time.UnixMilli(v.ModTimeMs)},
				Perms: perms,
			})
		}
		a.finishList(w, r, p, self, visible, o)
	case 2: // an item is a file: listing it is the same 400 a file gets on disk
		writeError(w, http.StatusBadRequest, "not_a_dir", "path is not a directory")
	default: // deeper than the tree defines
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
	}
}

// resolveClipItem turns /tenants/{T}/clipboards/{feed}/{entry} into a
// real target path and rewrites the request's ?path= to it, so the
// normal content/raw pipeline (ACL check, range, ETag, binary guard)
// answers for the link. Absence, dead links and unparseable entries
// all answer here; the target's ACL is then enforced by the normal
// pipeline itself.
func (a *API) resolveClipItem(w http.ResponseWriter, r *http.Request, tn string, rest []string) bool {
	if len(rest) < 2 {
		writeError(w, http.StatusBadRequest, "is_a_dir", "use /api/v1/fs to list directories")
		return false
	}
	if len(rest) > 2 {
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
		return false
	}
	tenant, err := a.Auth.TenantByName(r.Context(), tn)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
		return false
	}
	u := userFrom(r.Context())
	if !u.IsSuper && u.TenantID != tenant.ID {
		writeError(w, http.StatusForbidden, "forbidden", "clipboards belong to their tenant")
		return false
	}
	c, found, err := a.Clips.ByName(r.Context(), tenant.ID, rest[0])
	if err != nil {
		slog.Error("clipboard lookup (read)", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return false
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
		return false
	}
	id, ok := parseEntryName(rest[1])
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
		return false
	}
	it, found, err := a.Clips.ItemByID(r.Context(), c.ID, id)
	if err != nil {
		slog.Error("clipboard item lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return false
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "path does not exist")
		return false
	}
	if _, err := a.FS.Stat(it.Path); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "this clipboard link is dead: the file is gone")
		return false
	}
	q := r.URL.Query()
	q.Set("path", it.Path)
	r.URL.RawQuery = q.Encode()
	return true
}

// homeReadyPath returns a tenant's home path when that home exists on
// disk; feed grant paths are only meaningful under it.
func (a *API) homeReadyPath(ctx context.Context, tenantName string) (string, error) {
	home := auth.HomePath(tenantName)
	if _, err := a.FS.Stat(home); err != nil {
		return "", err
	}
	return home, nil
}
