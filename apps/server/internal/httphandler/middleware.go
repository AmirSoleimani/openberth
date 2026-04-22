package httphandler

import (
	"net/http"

	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// authed wraps a handler that needs an authenticated user but no JSON body.
// It performs auth, calls fn with the user, writes the returned value as JSON
// on success, or the error via writeErr on failure.
//
// Use for GET/DELETE-style endpoints whose inputs come from the path or query
// and whose output is a JSON object. For endpoints that decode a request body
// use authedJSON instead.
func authed(h *Handlers, w http.ResponseWriter, r *http.Request, fn func(*store.User) (any, error)) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}
	result, err := fn(user)
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, result)
}

// authedJSON wraps a handler that needs auth plus a decoded JSON body.
// Req is the expected request shape; the decoded value is passed to fn along
// with the user. On decode failure a 400 is written. On fn error writeErr is
// called. On success the returned value is encoded as JSON at 200.
func authedJSON[Req any](h *Handlers, w http.ResponseWriter, r *http.Request, fn func(*store.User, Req) (any, error)) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}
	req, ok := decodeJSON[Req](w, r)
	if !ok {
		return
	}
	result, err := fn(user, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, result)
}

// admin is the admin-gated variant of authed. Use for admin-only endpoints
// whose inputs come from the path or query (GET, DELETE, rotate-style).
func admin(h *Handlers, w http.ResponseWriter, r *http.Request, fn func(*store.User) (any, error)) {
	user := h.requireAdmin(w, r)
	if user == nil {
		return
	}
	result, err := fn(user)
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, result)
}

// adminJSON is the admin-gated variant of authedJSON. Callers reach here only
// when the caller is an authenticated admin.
func adminJSON[Req any](h *Handlers, w http.ResponseWriter, r *http.Request, fn func(*store.User, Req) (any, error)) {
	user := h.requireAdmin(w, r)
	if user == nil {
		return
	}
	req, ok := decodeJSON[Req](w, r)
	if !ok {
		return
	}
	result, err := fn(user, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, result)
}
