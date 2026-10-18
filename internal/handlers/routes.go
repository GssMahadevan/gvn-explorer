package handlers

import (
	"net/http"
	"time"
)

func (a *API) Register(mux *http.ServeMux) {
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

	// auth
	mux.HandleFunc("POST /api/v1/auth/login", a.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", a.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/me", a.requireAuth(a.handleMe))

	// file explorer
	mux.HandleFunc("GET /api/v1/fs", a.requireAuth(a.handleList))
	mux.HandleFunc("GET /api/v1/fs/content", a.requireAuth(a.handleContent))
	mux.HandleFunc("GET /api/v1/fs/raw", a.requireAuth(a.handleRaw))
	mux.HandleFunc("POST /api/v1/fs/mkdir", a.requireAuth(a.handleMkdir))
	mux.HandleFunc("POST /api/v1/fs/write", a.requireAuth(a.handleWrite))
	mux.HandleFunc("POST /api/v1/fs/rename", a.requireAuth(a.handleRename))
	mux.HandleFunc("POST /api/v1/fs/upload", a.requireAuth(a.handleUpload))
	mux.HandleFunc("DELETE /api/v1/fs", a.requireAuth(a.handleDelete))

	// tenants (platform super admin only)
	mux.HandleFunc("GET /api/v1/tenants", a.requireSuper(a.handleTenantsList))
	mux.HandleFunc("POST /api/v1/tenants", a.requireSuper(a.handleTenantsCreate))
	mux.HandleFunc("DELETE /api/v1/tenants/{id}", a.requireSuper(a.handleTenantsDelete))

	// perms (show: any logged-in user; mutate: admin, tenant-scoped)
	mux.HandleFunc("GET /api/v1/perms", a.requireAuth(a.handlePermsShow))
	mux.HandleFunc("PUT /api/v1/perms", a.requireAdmin(a.handlePermsPut))
	mux.HandleFunc("DELETE /api/v1/perms/{id}", a.requireAdmin(a.handlePermsDelete))

	// users & groups (admin; tenant admins see and manage only their tenant)
	mux.HandleFunc("GET /api/v1/users", a.requireAdmin(a.handleUsersList))
	mux.HandleFunc("POST /api/v1/users", a.requireAdmin(a.handleUsersCreate))
	mux.HandleFunc("DELETE /api/v1/users/{id}", a.requireAdmin(a.handleUsersDelete))
	mux.HandleFunc("POST /api/v1/users/{id}/password", a.requireAdmin(a.handleUsersPassword))
	mux.HandleFunc("GET /api/v1/groups", a.requireAdmin(a.handleGroupsList))
	mux.HandleFunc("POST /api/v1/groups", a.requireAdmin(a.handleGroupsCreate))
	mux.HandleFunc("DELETE /api/v1/groups/{id}", a.requireAdmin(a.handleGroupsDelete))
	mux.HandleFunc("POST /api/v1/groups/{id}/members", a.requireAdmin(a.handleGroupAddMember))
	mux.HandleFunc("DELETE /api/v1/groups/{id}/members/{userId}", a.requireAdmin(a.handleGroupRemoveMember))
}
