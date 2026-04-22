package httphandler

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/service"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

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
		Domain:   h.svc.Cfg.Domain,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
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
		Domain:   h.svc.Cfg.Domain,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// AuthCheck is used by Caddy forward_auth for SSO-protected deployments.
func (h *Handlers) AuthCheck(w http.ResponseWriter, r *http.Request) {
	user := h.Authenticate(r)
	if user != nil {
		subdomain := r.URL.Query().Get("subdomain")
		if subdomain != "" {
			deploy, _ := h.svc.Store.GetDeploymentBySubdomain(subdomain)
			if deploy != nil && deploy.AccessUsers != "" {
				if !service.CanMutateDeploy(deploy, user) {
					allowed := strings.Split(deploy.AccessUsers, ",")
					found := false
					for _, u := range allowed {
						if strings.TrimSpace(u) == user.Name {
							found = true
							break
						}
					}
					if !found {
						http.Error(w, "Forbidden", http.StatusForbidden)
						return
					}
				}
			}
		}
		w.Header().Set("X-OpenBerth-User", user.Name)
		w.WriteHeader(200)
		return
	}

	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	uri := r.Header.Get("X-Forwarded-Uri")
	originalURL := scheme + "://" + host + uri

	loginURL := h.svc.Cfg.BaseURL + "/login?redirect=" + url.QueryEscape(originalURL)
	http.Redirect(w, r, loginURL, http.StatusTemporaryRedirect)
}
