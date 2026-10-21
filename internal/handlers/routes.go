package handlers

import (
	"net/http"
	"time"
)

func (a *API) Register(mux *http.ServeMux) {
	a.principalLim = newLimiterSet(a.RPS, a.RBurst)
	a.authIP = newLimiterSet(a.AuthRPS, a.AuthBurst)
	a.authSlots = newAuthSlots(a.AuthConcurrency)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		var n int
		_ = a.Auth.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"users":  n,
			"root":   a.FS.Root(),
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// auth (login is per-IP rate limited AND globally capped: bcrypt is
	// expensive, brute-force must be too — see authSlots in rate.go)
	mux.HandleFunc("POST /api/v1/auth/login", a.perIP(a.authSlots.wrap(a.handleLogin)))
	mux.HandleFunc("POST /api/v1/auth/logout", a.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/me", a.requireAuth(a.handleMe))

	// device pairing: admins open invites, devices redeem them publicly
	// (join pays for a bcrypt too, so it shares both guards)
	mux.HandleFunc("POST /api/v1/pairings", a.requireAdmin(a.handlePairingsCreate))
	mux.HandleFunc("GET /api/v1/pairings", a.requireAdmin(a.handlePairingsList))
	mux.HandleFunc("DELETE /api/v1/pairings/{id}", a.requireAdmin(a.handlePairingsDelete))
	mux.HandleFunc("POST /api/v1/join", a.perIP(a.authSlots.wrap(a.handleJoin)))

	// file explorer
	mux.HandleFunc("GET /api/v1/fs", a.requireAuth(a.handleList))
	mux.HandleFunc("GET /api/v1/fs/content", a.requireAuth(a.handleContent))
	mux.HandleFunc("GET /api/v1/fs/raw", a.requireAuth(a.handleRaw))
	mux.HandleFunc("POST /api/v1/fs/mkdir", a.requireAuth(a.handleMkdir))
	mux.HandleFunc("POST /api/v1/fs/write", a.requireAuth(a.handleWrite))
	mux.HandleFunc("POST /api/v1/fs/rename", a.requireAuth(a.handleRename))
	mux.HandleFunc("POST /api/v1/fs/upload", a.requireAuth(a.handleUpload))
	mux.HandleFunc("DELETE /api/v1/fs", a.requireAuth(a.handleDelete))

	// draft/publish working copies (config editor): reading a draft takes
	// the r bit; saving, discarding and committing take the w bit, since
	// even drafting is a write action on the file
	mux.HandleFunc("GET /api/v1/fs/draft", a.requireAuth(a.handleDraftGet))
	mux.HandleFunc("PUT /api/v1/fs/draft", a.requireAuth(a.handleDraftPut))
	mux.HandleFunc("DELETE /api/v1/fs/draft", a.requireAuth(a.handleDraftDelete))
	mux.HandleFunc("POST /api/v1/fs/draft/commit", a.requireAuth(a.handleDraftCommit))

	// tenants (platform super admin only)
	mux.HandleFunc("GET /api/v1/tenants", a.requireSuper(a.handleTenantsList))
	mux.HandleFunc("POST /api/v1/tenants", a.requireSuper(a.handleTenantsCreate))
	mux.HandleFunc("PUT /api/v1/tenants/{id}", a.requireSuper(a.handleTenantsUpdate))
	mux.HandleFunc("DELETE /api/v1/tenants/{id}", a.requireSuper(a.handleTenantsDelete))
	// the tenant-wide kill switch: logins, live sessions and API keys of a
	// suspended tenant stop working; rows, grants and history remain
	mux.HandleFunc("POST /api/v1/tenants/{id}/active", a.requireSuper(a.handleTenantsActive))

	// perms (show: any logged-in user; mutate: admin, tenant-scoped)
	mux.HandleFunc("GET /api/v1/perms", a.requireAuth(a.handlePermsShow))
	mux.HandleFunc("PUT /api/v1/perms", a.requireAdmin(a.handlePermsPut))
	mux.HandleFunc("DELETE /api/v1/perms/{id}", a.requireAdmin(a.handlePermsDelete))

	// users & groups (admin; tenant admins see and manage only their tenant)
	mux.HandleFunc("GET /api/v1/users", a.requireAdmin(a.handleUsersList))
	mux.HandleFunc("POST /api/v1/users", a.requireAdmin(a.handleUsersCreate))
	mux.HandleFunc("DELETE /api/v1/users/{id}", a.requireAdmin(a.handleUsersDelete))
	mux.HandleFunc("POST /api/v1/users/{id}/password", a.requireAdmin(a.handleUsersPassword))
	// description edits for users (names are immutable; rename = recreate)
	mux.HandleFunc("PUT /api/v1/users/{id}", a.requireAdmin(a.handleUsersUpdate))
	// suspension kill switch: inactive users cannot log in, their sessions
	// die and their bearer keys stop working — but rows and history remain
	mux.HandleFunc("POST /api/v1/users/{id}/active", a.requireAdmin(a.handleUsersActive))
	// auth factors (API keys today; ed25519 / oauth2 reserved): provisioning
	// for device users, revocation kills access on the very next request
	mux.HandleFunc("GET /api/v1/users/{id}/factors", a.requireAdmin(a.handleFactorsList))
	mux.HandleFunc("POST /api/v1/users/{id}/factors", a.requireAdmin(a.handleFactorsCreate))
	mux.HandleFunc("DELETE /api/v1/users/{id}/factors/{fid}", a.requireAdmin(a.handleFactorsDelete))
	mux.HandleFunc("GET /api/v1/groups", a.requireAdmin(a.handleGroupsList))
	mux.HandleFunc("POST /api/v1/groups", a.requireAdmin(a.handleGroupsCreate))
	mux.HandleFunc("DELETE /api/v1/groups/{id}", a.requireAdmin(a.handleGroupsDelete))
	mux.HandleFunc("PUT /api/v1/groups/{id}", a.requireAdmin(a.handleGroupsUpdate))
	mux.HandleFunc("POST /api/v1/groups/{id}/members", a.requireAdmin(a.handleGroupAddMember))
	mux.HandleFunc("DELETE /api/v1/groups/{id}/members/{userId}", a.requireAdmin(a.handleGroupRemoveMember))
	// suspending a group makes every grant through it inert
	mux.HandleFunc("POST /api/v1/groups/{id}/active", a.requireAdmin(a.handleGroupsActive))

	// forensic trail: newest-first, scoped to the caller's tenant
	mux.HandleFunc("GET /api/v1/events", a.requireAdmin(a.handleEventsList))
}
