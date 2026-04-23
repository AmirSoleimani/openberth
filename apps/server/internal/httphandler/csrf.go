package httphandler

import (
	"net/http"
	"strings"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
)

// CSRFMiddleware rejects cross-site unsafe-method requests that rely on
// cookie auth. Bearer-token / API-key requests bypass the check entirely
// because those credentials aren't sent by the browser automatically —
// CSRF doesn't apply to them.
//
// Rationale: SameSite=Lax on openberth_session already blocks most
// cross-site cookie delivery, but it's browser-dependent and has the
// Lax-Allow-Unsafe loophole (some Chromium versions release cookies on
// POSTs within ~2 minutes of a top-level navigation). An explicit
// Origin/Referer check belts-and-braces over SameSite.
//
// Passes through in three cases:
//
//  1. Safe methods (GET, HEAD, OPTIONS) — no state change, no CSRF concern.
//  2. Authorization or X-API-Key header present — non-browser caller,
//     credential not auto-sent, so CSRF doesn't apply.
//  3. Caddy-only internal surfaces (/internal/*, /_data/*) — credentials
//     aren't cookie-based on those paths; they're forwarded by Caddy from
//     tenant subdomains or the loopback auth-check flow.
func CSRFMiddleware(cfg *config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isUnsafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "" {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if strings.HasPrefix(p, "/internal/") ||
			strings.HasPrefix(p, "/_data/") || p == "/_data" {
			next.ServeHTTP(w, r)
			return
		}
		if !originMatchesBaseURL(r, cfg.BaseURL) {
			http.Error(w, "CSRF: Origin/Referer does not match this server", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isUnsafeMethod reports whether m is a state-changing HTTP method per
// RFC 9110. The "safe" methods (GET, HEAD, OPTIONS, and idempotent+no-
// body methods) are exempt from CSRF checks because they shouldn't
// mutate server state.
func isUnsafeMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// originMatchesBaseURL validates that the request's Origin (preferred)
// or Referer (fallback) comes from our own BaseURL. Browsers always send
// one of these on cross-origin unsafe requests, so a request with
// neither is treated as CSRF and rejected — legitimate non-browser
// callers go through the bearer-token path and skip this middleware.
func originMatchesBaseURL(r *http.Request, baseURL string) bool {
	if baseURL == "" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == baseURL
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		return referer == baseURL ||
			strings.HasPrefix(referer, baseURL+"/") ||
			strings.HasPrefix(referer, baseURL+"?") ||
			strings.HasPrefix(referer, baseURL+"#")
	}
	return false
}
