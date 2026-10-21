package handlers

// Device pairing endpoints. An admin opens a short-lived invite
// (POST /api/v1/pairings), the joining device redeems it publicly
// (POST /api/v1/join) with the claim token from the QR code or the
// short code typed by hand, and the redeem creates a plain account and
// logs the new device in right away (session cookie, same as login).
// The invite's claim secrets appear only in the create response; list
// and audit views expose metadata and derived status only.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GssMahadevan/gvn-explorer/internal/audit"
	"github.com/GssMahadevan/gvn-explorer/internal/auth"
)

var pairingsListSpec = ListSpec{
	Sorts:        map[string]bool{"created": true, "expires": true, "description": true, "username": true, "used": true, "tenant": true},
	Cols:         []string{"description", "username", "tenant"},
	DefSort:      "created",
	DefDir:       "desc",
	TenantFilter: true,
}

type pairingBody struct {
	Description string `json:"description"`
	Username    string `json:"username"` // optional preset login for the join
	Tenant      string `json:"tenant"`   // super admins may target any tenant
	TTLSeconds  int    `json:"ttl_seconds"`
	APIKey      bool   `json:"apikey"` // also hand the device an API key
	Expiry             // applied to the *created account*, not the invite
}

func (a *API) handlePairingsCreate(w http.ResponseWriter, r *http.Request) {
	var b pairingBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	b.Description = strings.TrimSpace(b.Description)
	if b.Description == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "description required (what device is this?)")
		return
	}
	b.Username = strings.TrimSpace(b.Username)
	ttl := time.Duration(b.TTLSeconds) * time.Second
	if b.TTLSeconds == 0 {
		ttl = auth.PairingTTLDefault
	} else if ttl < 30*time.Second || ttl > auth.PairingTTLMax {
		writeError(w, http.StatusUnprocessableEntity, "validation", "ttl_seconds must be 30..86400")
		return
	}
	exp, err := b.Expiry.resolve()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation", err.Error())
		return
	}
	u := userFrom(r.Context())
	tid, err := a.Auth.TenantID(r.Context(), resolveCreateTenant(u, b.Tenant))
	if err != nil {
		writeError(w, http.StatusConflict, "exists", "unknown tenant")
		return
	}
	p, token, code, err := a.Auth.CreatePairing(r.Context(), tid, b.Description, b.Username, b.APIKey, exp, ttl, u.ID)
	switch {
	case errors.Is(err, auth.ErrUserTaken):
		writeError(w, http.StatusConflict, "exists", "username already exists in tenant")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	details := map[string]any{"ttl_seconds": int(ttl.Seconds()), "apikey": b.APIKey}
	if exp != nil {
		details["user_expires_at"] = exp.Format(time.RFC3339)
	}
	a.audit(r, audit.Event{Action: "pair_create",
		TargetType: "pairing", TargetID: p.ID, TargetName: p.Description, Details: details})
	// token and code are shown exactly once, like an API key's plaintext.
	writeJSON(w, http.StatusCreated, struct {
		Pairing auth.Pairing `json:"pairing"`
		Token   string       `json:"token"`
		Code    string       `json:"code"`
	}{p, token, code})
}

func (a *API) handlePairingsList(w http.ResponseWriter, r *http.Request) {
	o, ok := parseList(w, r, pairingsListSpec)
	if !ok {
		return
	}
	ps, total, err := a.Auth.ListPairings(r.Context(), scopeTenantID(userFrom(r.Context())), o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	setTotal(w, total)
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) handlePairingsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "id must be numeric")
		return
	}
	// scopeTenantID: 0 for super admins (any pairing), own tenant otherwise.
	if err := a.Auth.RevokePairing(r.Context(), id, scopeTenantID(userFrom(r.Context()))); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "no unused pairing with that id (in scope)")
		return
	}
	a.audit(r, audit.Event{Action: "pair_revoke", TargetType: "pairing", TargetID: id})
	w.WriteHeader(http.StatusNoContent)
}

type joinBody struct {
	Token string `json:"token"` // from the QR code (URL fragment)
	Code  string `json:"code"`  // typed by hand
}

// handleJoin redeems a claim secret. Public and rate limited like login
// (it costs a bcrypt generation plus the invite's other work); ambient
// credentials are irrelevant here, the secret travels in the JSON body,
// so CSRF does not apply.
func (a *API) handleJoin(w http.ResponseWriter, r *http.Request) {
	var b joinBody
	if !a.decodeBody(w, r, &b) {
		return
	}
	b.Token = strings.TrimSpace(b.Token)
	b.Code = strings.TrimSpace(b.Code)
	secret := b.Token
	if b.Code != "" {
		if b.Token != "" {
			writeError(w, http.StatusUnprocessableEntity, "validation", "provide token or code, not both")
			return
		}
		secret = b.Code
	}
	if secret == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation", "token or code required")
		return
	}
	p, u, password, apikey, err := a.Auth.RedeemPairing(r.Context(), secret)
	switch {
	case errors.Is(err, auth.ErrPairNotFound):
		writeError(w, http.StatusNotFound, "not_found", "invalid pairing code")
		return
	case errors.Is(err, auth.ErrPairExpired):
		writeError(w, http.StatusGone, "gone", "this pairing has expired; ask for a new one")
		return
	case errors.Is(err, auth.ErrPairUsed):
		writeError(w, http.StatusConflict, "conflict", "this pairing was already used or revoked")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal", "join failed")
		return
	}
	ttl := a.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	token, err := a.Auth.CreateSession(r.Context(), u.ID, ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "session creation failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
	// Two audit events: the invite consumed, and the account it produced
	// (marked as pairing-born so the usual user_create forensic still holds).
	a.audit(r, audit.Event{Action: "pair_join", Actor: u.Tenant + "/" + u.Username,
		TenantID: p.TenantID, TargetType: "pairing", TargetID: p.ID, TargetName: p.Description,
		Details: map[string]any{"user_id": u.ID, "username": u.Username, "apikey": apikey != ""}})
	a.audit(r, audit.Event{Action: "user_create", Actor: u.Tenant + "/" + u.Username,
		TenantID: u.TenantID, TargetType: "user", TargetID: u.ID, TargetName: u.Username,
		Details: map[string]any{"via": "pairing", "pairing_id": p.ID}})
	writeJSON(w, http.StatusOK, struct {
		User     auth.User `json:"user"`
		Password string    `json:"password"`
		APIKey   string    `json:"apikey,omitempty"`
	}{u, password, apikey})
}
