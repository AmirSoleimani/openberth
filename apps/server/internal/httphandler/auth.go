package httphandler

import (
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/service"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// validSubdomain matches the format emitted by SanitizeName: lowercase
// alphanumerics and hyphens, up to 63 characters. AuthCheck rebuilds the
// tenant URL from the ?subdomain query param, so this check is a trust
// boundary between Caddy's forward_auth and the redirect we emit.
var validSubdomain = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

// sanitizeForwardedURI keeps only the path + query portion of a tenant-
// controlled X-Forwarded-Uri. Falls back to "/" on anything malformed —
// scheme, host, protocol-relative, or backslashed input.
func sanitizeForwardedURI(u string) string {
	if u == "" || u[0] != '/' {
		return "/"
	}
	if strings.HasPrefix(u, "//") || strings.ContainsRune(u, '\\') {
		return "/"
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host != "" || parsed.Scheme != "" {
		return "/"
	}
	out := parsed.Path
	if out == "" {
		out = "/"
	}
	if parsed.RawQuery != "" {
		out += "?" + parsed.RawQuery
	}
	return out
}

// ── CORS ─────────────────────────────────────────────────────────────

func SetCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, Mcp-Session-Id, X-API-Key")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// CORS wraps a HandlerFunc with CORS preflight + headers.
func CORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		SetCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next(w, r)
	}
}

// CORSHandler wraps an http.Handler with CORS preflight + headers.
func CORSHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Authentication ───────────────────────────────────────────────────

// Authenticate checks for a valid API key or session cookie and returns the user.
// Exported so MCP handler and OAuth handler can use it.
func (h *Handlers) Authenticate(r *http.Request) *store.User {
	// 1. Check API key (header or bearer token)
	key := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		key = auth[7:]
	}
	if key == "" {
		key = r.Header.Get("X-API-Key")
	}
	if key != "" {
		if strings.HasPrefix(key, "sc_") {
			user, err := h.svc.Store.GetUserByKey(key)
			if err == nil {
				return user
			}
		}
		if user, _ := h.svc.Store.GetUserByOAuthToken(key); user != nil {
			return user
		}
	}

	// 2. Check session cookie
	cookie, err := r.Cookie("openberth_session")
	if err == nil && strings.HasPrefix(cookie.Value, "ses_") {
		user, _ := h.svc.Store.GetUserBySession(cookie.Value)
		return user
	}

	return nil
}

// requireAuth checks authentication and writes a 401 response if not authenticated.
func (h *Handlers) requireAuth(w http.ResponseWriter, r *http.Request) *store.User {
	user := h.Authenticate(r)
	if user == nil {
		jsonErr(w, 401, "Missing or invalid API key. Use Authorization: Bearer <key>")
		return nil
	}
	return user
}

// requireAdmin checks authentication and admin role, writing an error response if not.
func (h *Handlers) requireAdmin(w http.ResponseWriter, r *http.Request) *store.User {
	user := h.requireAuth(w, r)
	if user == nil {
		return nil
	}
	if user.Role != "admin" {
		jsonErr(w, 403, "Admin access required.")
		return nil
	}
	return user
}

// createSession generates a session token, stores it, and sets the cookie.
//
// The cookie is intentionally host-only (no Domain attribute) so a browser
// will never send it to tenant subdomains. Tenant apps that need SSO auth
// go through MintSSOToken + SSORedirect + AuthCheck instead — see C-1 in
// the security audit plan for why the Domain=<root> shape was replaced.
// SameSite=Strict is safe now that the cookie never needs to follow a
// cross-subdomain redirect.
func (h *Handlers) createSession(w http.ResponseWriter, userID string) string {
	token := "ses_" + service.RandomHex(32)
	expiresAt := time.Now().Add(7 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if err := h.svc.Store.CreateSession(token, userID, expiresAt); err != nil {
		log.Printf("[session] Failed to create session for user %s: %v", userID, err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "openberth_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   7 * 24 * 60 * 60,
	})
	return token
}

// clearSession deletes the session from DB and clears the cookie.
func (h *Handlers) clearSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("openberth_session")
	if err == nil {
		h.svc.Store.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "openberth_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// ssoTokenQueryParam is the URL query key used to hand an SSO token from
// the UI host back to the tenant subdomain on first visit.
const ssoTokenQueryParam = "__berth_sso"

// ssoTenantCookieName is the host-scoped cookie that carries the signed
// SSO token after the first handoff. Short-lived; signed; keyed to the
// specific tenant subdomain via the master-key derivation in service.MintSSOToken.
const ssoTenantCookieName = "berth_sso"

// AuthCheck is used by Caddy forward_auth for SSO-protected deployments.
//
// The trusted identifier of which deployment is asking is the ?subdomain
// query param — set by our Caddy forward_auth template, not by the
// user-agent. X-Forwarded-Host / X-Forwarded-Proto come from the tenant's
// own request and must not be trusted when building the post-login return
// URL (that would let an attacker craft a login page whose redirect points
// to an external domain).
//
// Tenant auth never relies on the UI's openberth_session cookie — that
// cookie is host-only to the UI. Instead, the UI mints a short-lived signed
// token (service.MintSSOToken) keyed to the tenant subdomain via the master
// key. The tenant receives the token once via ?__berth_sso=…, AuthCheck
// strips it and sets a tenant-scoped cookie, and subsequent requests carry
// the cookie. See C-1 in the security-audit plan for the threat model.
func (h *Handlers) AuthCheck(w http.ResponseWriter, r *http.Request) {
	subdomain := r.URL.Query().Get("subdomain")
	if !validSubdomain.MatchString(subdomain) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	masterKey, mkErr := h.svc.Cfg.GetMasterKeyBytes()
	if mkErr != nil {
		http.Error(w, "Server misconfigured", http.StatusInternalServerError)
		return
	}

	// Path 1: first-hit handoff. The UI just redirected the browser here
	// with a fresh token in the URL. Validate it, set the tenant cookie,
	// and 302 the browser back to the same URL minus the token so the
	// address bar is clean and the tenant app doesn't see the token.
	forwardedURI := r.Header.Get("X-Forwarded-Uri")
	if tokenFromURI := extractSSOTokenFromURI(forwardedURI); tokenFromURI != "" {
		if userID, err := service.VerifySSOToken(masterKey, subdomain, tokenFromURI); err == nil {
			user, _ := h.svc.Store.GetUserByID(userID)
			if user != nil && h.tenantAccessAllowed(user, subdomain) {
				h.setTenantSSOCookie(w, tokenFromURI)
				cleanURI := stripSSOTokenFromURI(sanitizeForwardedURI(forwardedURI))
				http.Redirect(w, r, h.tenantURL(subdomain, cleanURI), http.StatusFound)
				return
			}
		}
		// Bad token → fall through to Path 3 (redirect to UI handoff).
	}

	// Path 2: returning visitor. Tenant-scoped cookie set on a prior visit.
	if cookie, err := r.Cookie(ssoTenantCookieName); err == nil && cookie.Value != "" {
		if userID, err := service.VerifySSOToken(masterKey, subdomain, cookie.Value); err == nil {
			user, _ := h.svc.Store.GetUserByID(userID)
			if user != nil && h.tenantAccessAllowed(user, subdomain) {
				w.Header().Set("X-OpenBerth-User", user.Name)
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		// Cookie invalid/expired/ACL-changed → fall through.
	}

	// Path 3: no valid tenant auth. Send the browser to the UI host for a
	// handoff. The UI will check the user's real session and mint a fresh
	// token, then redirect back here with it in the URL (Path 1).
	returnPath := sanitizeForwardedURI(forwardedURI)
	ssoURL := h.svc.Cfg.BaseURL + "/auth/sso-redirect?subdomain=" + subdomain +
		"&return=" + url.QueryEscape(returnPath)
	http.Redirect(w, r, ssoURL, http.StatusTemporaryRedirect)
}

// SSORedirect is the UI-side partner of AuthCheck. A browser that tried
// to visit a tenant subdomain and lacked a tenant cookie gets redirected
// here. We authenticate against the real session, mint a subdomain-scoped
// signed token, and send the browser back to the tenant with the token.
//
// If the caller isn't logged into the UI, bounce to /login preserving the
// return chain so they land back on this handler after auth completes.
func (h *Handlers) SSORedirect(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	subdomain := q.Get("subdomain")
	if !validSubdomain.MatchString(subdomain) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	returnPath := q.Get("return")
	if !isLocalRedirect(returnPath) {
		returnPath = "/"
	}

	user := h.Authenticate(r)
	if user == nil {
		loginURL := "/login?redirect=" + url.QueryEscape(r.URL.String())
		http.Redirect(w, r, loginURL, http.StatusTemporaryRedirect)
		return
	}

	if !h.tenantAccessAllowed(user, subdomain) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	masterKey, err := h.svc.Cfg.GetMasterKeyBytes()
	if err != nil {
		http.Error(w, "Server misconfigured", http.StatusInternalServerError)
		return
	}
	token := service.MintSSOToken(masterKey, subdomain, user.ID, service.SSOTokenTTL)
	target := h.tenantURL(subdomain, appendSSOTokenToURI(returnPath, token))
	http.Redirect(w, r, target, http.StatusFound)
}

// tenantAccessAllowed reports whether user may reach subdomain's
// SSO-protected entry point. Owners always pass; otherwise the user must
// be in the deployment's access_users list.
func (h *Handlers) tenantAccessAllowed(user *store.User, subdomain string) bool {
	deploy, _ := h.svc.Store.GetDeploymentBySubdomain(subdomain)
	if deploy == nil {
		// No deployment record — probably a race or a stale Caddy config.
		// Deny by default; the user can retry once the deploy catches up.
		return false
	}
	if service.CanMutateDeploy(deploy, user) {
		return true
	}
	if deploy.AccessUsers == "" {
		// No ACL configured — fall back to owner-only access (already
		// handled above). Non-owners can't reach SSO-protected deploys
		// without being listed.
		return false
	}
	for _, u := range strings.Split(deploy.AccessUsers, ",") {
		if strings.TrimSpace(u) == user.Name {
			return true
		}
	}
	return false
}

// setTenantSSOCookie stores the signed token in a host-only cookie so it
// scopes to this specific tenant subdomain and nowhere else.
func (h *Handlers) setTenantSSOCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     ssoTenantCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		// Lax (not Strict) so the cookie is sent on the UI's 302 back to the
		// tenant host — that top-level navigation is effectively cross-site
		// relative to the tenant cookie domain.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(service.SSOTokenTTL.Seconds()),
	})
}

// tenantURL composes an absolute URL for a path on the given tenant
// subdomain, respecting the Insecure config flag for dev installs.
func (h *Handlers) tenantURL(subdomain, path string) string {
	scheme := "https"
	if h.svc.Cfg.Insecure {
		scheme = "http"
	}
	if path == "" {
		path = "/"
	}
	return scheme + "://" + subdomain + "." + h.svc.Cfg.Domain + path
}

// extractSSOTokenFromURI returns the value of the __berth_sso query param
// in uri, or "" if absent. uri is expected to be the path+query portion
// (starting with '/' or '?') — we never trust a full URL here.
func extractSSOTokenFromURI(uri string) string {
	q := uriQuery(uri)
	if q == "" {
		return ""
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return ""
	}
	return vals.Get(ssoTokenQueryParam)
}

// stripSSOTokenFromURI returns uri with the __berth_sso query param
// removed. Other params keep their positions.
func stripSSOTokenFromURI(uri string) string {
	q := uriQuery(uri)
	if q == "" {
		return uri
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return uri
	}
	if _, ok := vals[ssoTokenQueryParam]; !ok {
		return uri
	}
	vals.Del(ssoTokenQueryParam)
	path := uriPath(uri)
	encoded := vals.Encode()
	if encoded == "" {
		return path
	}
	return path + "?" + encoded
}

// appendSSOTokenToURI appends __berth_sso=<token> to uri, preserving any
// existing query string.
func appendSSOTokenToURI(uri, token string) string {
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return uri + sep + ssoTokenQueryParam + "=" + url.QueryEscape(token)
}

// uriQuery returns the raw query string (without the leading '?') from a
// path+query style URI. Returns "" when there's no query portion.
func uriQuery(uri string) string {
	if i := strings.Index(uri, "?"); i >= 0 {
		return uri[i+1:]
	}
	return ""
}

// uriPath returns the path component of a path+query URI.
func uriPath(uri string) string {
	if i := strings.Index(uri, "?"); i >= 0 {
		return uri[:i]
	}
	return uri
}
