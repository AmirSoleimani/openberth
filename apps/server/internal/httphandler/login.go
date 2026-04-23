package httphandler

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/AmirSoleimani/openberth/apps/server/internal/service"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// ── Redirect validation ──────────────────────────────────────────

// isLocalRedirect returns true if the URL is a relative path (e.g. "/gallery/").
// Rejects absolute URLs, protocol-relative URLs (//evil.com), and empty strings.
func isLocalRedirect(u string) bool {
	return u != "" && u[0] == '/' && (len(u) == 1 || u[1] != '/')
}

// isAllowedCallback returns true if the callback URL is safe for the CLI OAuth flow.
// Allows localhost URLs only (http://localhost:* or http://127.0.0.1:*).
func isAllowedCallback(u string) bool {
	return strings.HasPrefix(u, "http://localhost:") || strings.HasPrefix(u, "http://localhost/") ||
		strings.HasPrefix(u, "http://127.0.0.1:") || strings.HasPrefix(u, "http://127.0.0.1/")
}

// redirectWithLoginCode generates a login code and redirects to the callback URL.
// This consolidates the duplicated login-code generation pattern used by
// SetupSubmit, LoginPage, LoginSubmit, and OIDCCallback.
func (h *Handlers) redirectWithLoginCode(w http.ResponseWriter, r *http.Request, userID, callback string) {
	code := "lc_" + service.RandomHex(24)
	expiresAt := time.Now().Add(5 * time.Minute).UTC().Format("2006-01-02 15:04:05")
	h.svc.Store.CreateLoginCode(code, userID, callback, expiresAt)
	sep := "?"
	if strings.Contains(callback, "?") {
		sep = "&"
	}
	http.Redirect(w, r, callback+sep+"code="+code, http.StatusFound)
}

// ── Setup (one-time admin bootstrap) ─────────────────────────────

func (h *Handlers) SetupPage(w http.ResponseWriter, r *http.Request) {
	count, _ := h.svc.Store.CountUsers()
	if count > 0 {
		http.NotFound(w, r)
		return
	}

	callback := r.URL.Query().Get("callback")
	redirect := r.URL.Query().Get("redirect")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, setupPageHTML, html.EscapeString(redirect), html.EscapeString(callback))
}

func (h *Handlers) SetupSubmit(w http.ResponseWriter, r *http.Request) {
	count, _ := h.svc.Store.CountUsers()
	if count > 0 {
		http.NotFound(w, r)
		return
	}

	r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")
	callback := r.FormValue("callback")
	redirect := r.FormValue("redirect")

	if username == "" || password == "" {
		setupError(w, "Username and password are required.")
		return
	}
	if password != confirm {
		setupError(w, "Passwords do not match.")
		return
	}
	if len(password) < 8 {
		setupError(w, "Password must be at least 8 characters.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		jsonErr(w, 500, "Failed to hash password.")
		return
	}

	user := &store.User{
		ID:              "usr_" + service.RandomHex(8),
		Name:            username,
		APIKey:          service.NewAPIKey(),
		Role:            "admin",
		MaxDeployments:  h.svc.Cfg.DefaultMaxDeploy,
		DefaultTTLHours: h.svc.Cfg.DefaultTTLHours,
		DisplayName:     username,
		PasswordHash:    string(hash),
	}

	if err := h.svc.Store.CreateUser(user); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			jsonErr(w, 409, "User already exists.")
			return
		}
		jsonErr(w, 500, "Failed to create user.")
		return
	}
	h.svc.Store.UpdateUserPassword(user.ID, string(hash))

	h.createSession(w, user.ID)
	log.Printf("[setup] Admin user '%s' created", username)

	if callback != "" && isAllowedCallback(callback) {
		h.redirectWithLoginCode(w, r, user.ID, callback)
		return
	}
	// If setup was initiated from an OAuth consent flow (e.g. MCP login on
	// a fresh --no-web install), land on the OAuth endpoint so the client
	// finishes its authorization. Only safe-relative paths accepted.
	if redirect != "" && isLocalRedirect(redirect) {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}

	http.Redirect(w, r, "/gallery/", http.StatusFound)
}

// ── Login ────────────────────────────────────────────────────────

func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	// If no users exist, redirect to setup
	count, _ := h.svc.Store.CountUsers()
	if count == 0 {
		target := "/setup"
		if cb := r.URL.Query().Get("callback"); cb != "" {
			target += "?callback=" + cb
		} else if rd := r.URL.Query().Get("redirect"); rd != "" {
			target += "?redirect=" + rd
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	// If already logged in, handle callback or redirect
	user := h.Authenticate(r)
	if user != nil {
		if cb := r.URL.Query().Get("callback"); cb != "" && isAllowedCallback(cb) {
			h.redirectWithLoginCode(w, r, user.ID, cb)
			return
		}
		if rd := r.URL.Query().Get("redirect"); rd != "" && isLocalRedirect(rd) {
			http.Redirect(w, r, rd, http.StatusFound)
			return
		}
		http.Redirect(w, r, "/gallery/", http.StatusFound)
		return
	}

	redirect := r.URL.Query().Get("redirect")
	callback := r.URL.Query().Get("callback")
	errorMsg := r.URL.Query().Get("error")

	// Check if SSO is configured
	issuer, _ := h.svc.Store.GetSetting("oidc.issuer")
	showSSO := issuer != ""

	// SSO-only mode: auto-redirect to OIDC provider (unless showing an error)
	oidcMode, _ := h.svc.Store.GetSetting("oidc.mode")
	if showSSO && oidcMode == "sso_only" && errorMsg == "" {
		ssoLink := "/auth/oidc/start"
		if redirect != "" {
			ssoLink += "?redirect=" + redirect
		} else if callback != "" {
			ssoLink += "?callback=" + callback
		}
		http.Redirect(w, r, ssoLink, http.StatusFound)
		return
	}

	// SSO-only mode with error: show error page with retry link
	if showSSO && oidcMode == "sso_only" && errorMsg != "" {
		ssoLink := "/auth/oidc/start"
		if redirect != "" {
			ssoLink += "?redirect=" + redirect
		} else if callback != "" {
			ssoLink += "?callback=" + callback
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, ssoOnlyErrorPageHTML, html.EscapeString(errorMsg), ssoLink)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	ssoButton := ""
	if showSSO {
		ssoLink := "/auth/oidc/start"
		if redirect != "" {
			ssoLink += "?redirect=" + redirect
		} else if callback != "" {
			ssoLink += "?callback=" + callback
		}
		ssoButton = fmt.Sprintf(`<a href="%s" class="btn-outline">Login with SSO</a>
    <div class="divider">or</div>`, ssoLink)
	}

	errorBlock := ""
	if errorMsg != "" {
		errorBlock = fmt.Sprintf(`<div class="error">%s</div>`, html.EscapeString(errorMsg))
	}

	fmt.Fprintf(w, loginPageHTML, errorBlock, ssoButton, html.EscapeString(redirect), html.EscapeString(callback))
}

func (h *Handlers) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	redirect := r.FormValue("redirect")
	callback := r.FormValue("callback")

	// Block local login when SSO-only mode is active
	oidcMode, _ := h.svc.Store.GetSetting("oidc.mode")
	if oidcMode == "sso_only" {
		loginRedirectWithError(w, r, redirect, callback, "Local login is disabled. Use SSO.")
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if username == "" || password == "" {
		loginRedirectWithError(w, r, redirect, callback, "Username and password are required.")
		return
	}

	user, err := h.svc.Store.GetUserByName(username)
	if err != nil || user == nil || user.PasswordHash == "" {
		loginRedirectWithError(w, r, redirect, callback, "Invalid username or password.")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		loginRedirectWithError(w, r, redirect, callback, "Invalid username or password.")
		return
	}

	h.createSession(w, user.ID)
	log.Printf("[login] User '%s' logged in", username)

	if callback != "" && isAllowedCallback(callback) {
		h.redirectWithLoginCode(w, r, user.ID, callback)
		return
	}

	if redirect != "" && isLocalRedirect(redirect) {
		http.Redirect(w, r, redirect, http.StatusFound)
		return
	}
	http.Redirect(w, r, "/gallery/", http.StatusFound)
}

func loginRedirectWithError(w http.ResponseWriter, r *http.Request, redirect, callback, errMsg string) {
	target := "/login?error=" + errMsg
	if redirect != "" {
		target += "&redirect=" + redirect
	}
	if callback != "" {
		target += "&callback=" + callback
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// ── Logout ───────────────────────────────────────────────────────

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	h.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ── Login Code Exchange (for CLI) ────────────────────────────────

func (h *Handlers) LoginExchange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		jsonErr(w, 400, "Invalid request. Provide {\"code\": \"lc_...\"}.")
		return
	}

	lc, err := h.svc.Store.GetLoginCode(body.Code)
	if err != nil || lc == nil {
		jsonErr(w, 400, "Invalid or expired login code.")
		return
	}

	h.svc.Store.MarkLoginCodeUsed(body.Code)

	user, _ := h.svc.Store.GetUserByID(lc.UserID)
	if user == nil {
		jsonErr(w, 400, "User not found.")
		return
	}

	jsonResp(w, 200, map[string]string{
		"apiKey": user.APIKey,
		"name":   user.Name,
	})
}

// ── Change Password ──────────────────────────────────────────────

func (h *Handlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, 400, "Invalid JSON.")
		return
	}
	if len(body.NewPassword) < 8 {
		jsonErr(w, 400, "New password must be at least 8 characters.")
		return
	}

	// If user has an existing password, verify current password
	fullUser, _ := h.svc.Store.GetUserByID(user.ID)
	if fullUser != nil && fullUser.PasswordHash != "" {
		if body.CurrentPassword == "" {
			jsonErr(w, 400, "Current password is required.")
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(fullUser.PasswordHash), []byte(body.CurrentPassword)); err != nil {
			jsonErr(w, 403, "Current password is incorrect.")
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		jsonErr(w, 500, "Failed to hash password.")
		return
	}

	if err := h.svc.Store.UpdateUserPassword(user.ID, string(hash)); err != nil {
		jsonErr(w, 500, "Failed to update password.")
		return
	}

	jsonResp(w, 200, map[string]string{"message": "Password updated."})
}

// ── Rotate API Key ───────────────────────────────────────────────

func (h *Handlers) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	newKey := service.NewAPIKey()
	if err := h.svc.Store.UpdateUserAPIKey(user.ID, newKey); err != nil {
		jsonErr(w, 500, "Failed to rotate API key.")
		return
	}

	log.Printf("[rotate-key] User '%s' rotated API key", user.Name)
	jsonResp(w, 200, map[string]string{"apiKey": newKey})
}

// ── GET /api/me ──────────────────────────────────────────────────
//
// Returns the caller's bootstrap context. Used by the gallery SPA at load
// time, and by any client that wants to know its own identity/role/version.

func (h *Handlers) GetMe(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}
	displayName := user.DisplayName
	if displayName == "" {
		displayName = user.Name
	}
	jsonResp(w, 200, map[string]any{
		"id":            user.ID,
		"name":          user.Name,
		"displayName":   displayName,
		"role":          user.Role,
		"hasPassword":   user.PasswordHash != "",
		"serverVersion": h.version,
	})
}

func setupError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(400)
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>OpenBerth - Error</title><style>`+pageStyles+`</style></head><body><div class="card"><h1>OpenBerth</h1><p class="subtitle">Something went wrong.</p><div class="error">%s</div><a href="javascript:history.back()" class="btn-outline">Go back</a></div></body></html>`, html.EscapeString(msg))
}

// ── HTML Templates ──────────────────────────────────────────────

const pageStyles = `
  * { box-sizing: border-box; margin: 0; padding: 0; }
  :root {
    --bg: hsl(0 0%% 100%%); --fg: hsl(0 0%% 3.9%%);
    --card: hsl(0 0%% 100%%); --card-fg: hsl(0 0%% 3.9%%);
    --muted: hsl(0 0%% 96.1%%); --muted-fg: hsl(0 0%% 45.1%%);
    --border: hsl(0 0%% 89.8%%); --input: hsl(0 0%% 89.8%%);
    --primary: hsl(0 0%% 9%%); --primary-fg: hsl(0 0%% 98%%);
    --ring: hsl(0 0%% 3.9%%);
    --destructive: hsl(0 84.2%% 60.2%%);
    --radius: 0.5rem;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: hsl(0 0%% 3.9%%); --fg: hsl(0 0%% 98%%);
      --card: hsl(0 0%% 3.9%%); --card-fg: hsl(0 0%% 98%%);
      --muted: hsl(0 0%% 14.9%%); --muted-fg: hsl(0 0%% 63.9%%);
      --border: hsl(0 0%% 14.9%%); --input: hsl(0 0%% 14.9%%);
      --primary: hsl(0 0%% 98%%); --primary-fg: hsl(0 0%% 9%%);
      --ring: hsl(0 0%% 83.1%%);
      --destructive: hsl(0 62.8%% 30.6%%);
    }
  }
  body {
    font-family: ui-monospace, "Cascadia Code", "Source Code Pro", Menlo, Consolas, "DejaVu Sans Mono", monospace;
    background: var(--bg); color: var(--fg);
    display: flex; justify-content: center; align-items: center;
    min-height: 100vh; padding: 20px;
    -webkit-font-smoothing: antialiased;
  }
  .card {
    background: var(--card); color: var(--card-fg);
    border: 1px solid var(--border); border-radius: var(--radius);
    padding: 2.5rem; max-width: 24rem; width: 100%%;
    box-shadow: 0 1px 3px rgba(0,0,0,0.08);
  }
  h1 { font-size: 1.25rem; font-weight: 700; letter-spacing: -0.025em; }
  .subtitle { color: var(--muted-fg); font-size: 0.875rem; margin-top: 0.25rem; margin-bottom: 1.5rem; }
  .form-group { margin-bottom: 1rem; }
  label { display: block; font-weight: 500; font-size: 0.875rem; margin-bottom: 0.375rem; }
  input[type=text], input[type=password] {
    width: 100%%; padding: 0.5rem 0.75rem;
    background: transparent; color: var(--fg);
    border: 1px solid var(--input); border-radius: var(--radius);
    font-family: inherit; font-size: 0.875rem;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  input:focus { outline: none; border-color: var(--ring); box-shadow: 0 0 0 2px color-mix(in srgb, var(--ring) 20%%, transparent); }
  input::placeholder { color: var(--muted-fg); }
  .btn {
    display: block; width: 100%%; padding: 0.5rem 1rem; margin-top: 1.5rem;
    background: var(--primary); color: var(--primary-fg);
    border: none; border-radius: var(--radius);
    font-family: inherit; font-size: 0.875rem; font-weight: 500;
    cursor: pointer; transition: opacity 0.15s;
  }
  .btn:hover { opacity: 0.9; }
  .btn-outline {
    display: block; width: 100%%; padding: 0.5rem 1rem;
    background: transparent; color: var(--fg);
    border: 1px solid var(--border); border-radius: var(--radius);
    font-family: inherit; font-size: 0.875rem; font-weight: 500;
    cursor: pointer; text-align: center; text-decoration: none;
    transition: background 0.15s;
  }
  .btn-outline:hover { background: var(--muted); }
  .divider { display: flex; align-items: center; gap: 0.75rem; margin: 1rem 0; color: var(--muted-fg); font-size: 0.75rem; }
  .divider::before, .divider::after { content: ""; flex: 1; height: 1px; background: var(--border); }
  .error { background: color-mix(in srgb, var(--destructive) 10%%, transparent); border: 1px solid color-mix(in srgb, var(--destructive) 30%%, transparent); color: var(--destructive); padding: 0.625rem 0.75rem; border-radius: var(--radius); margin-bottom: 1rem; font-size: 0.875rem; }
`

const setupPageHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenBerth - Setup</title>
<style>` + pageStyles + `</style>
</head>
<body>
<div class="card">
  <h1>OpenBerth</h1>
  <p class="subtitle">Create your admin account to get started.</p>
  <form method="POST" action="/setup">
    <input type="hidden" name="redirect" value="%s">
    <input type="hidden" name="callback" value="%s">
    <div class="form-group">
      <label for="username">Username</label>
      <input type="text" id="username" name="username" placeholder="admin" required autofocus>
    </div>
    <div class="form-group">
      <label for="password">Password</label>
      <input type="password" id="password" name="password" placeholder="Min 8 characters" minlength="8" required>
    </div>
    <div class="form-group">
      <label for="confirm">Confirm Password</label>
      <input type="password" id="confirm" name="confirm" minlength="8" required>
    </div>
    <button type="submit" class="btn">Create Admin Account</button>
  </form>
</div>
</body>
</html>`

const ssoOnlyErrorPageHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenBerth - Login Error</title>
<style>` + pageStyles + `</style>
</head>
<body>
<div class="card">
  <h1>OpenBerth</h1>
  <p class="subtitle">Login failed.</p>
  <div class="error">%s</div>
  <a href="%s" class="btn-outline">Try again with SSO</a>
</div>
</body>
</html>`

const loginPageHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenBerth - Login</title>
<style>` + pageStyles + `</style>
</head>
<body>
<div class="card">
  <h1>OpenBerth</h1>
  <p class="subtitle">Sign in to continue.</p>
  %s
  %s
  <form method="POST" action="/login">
    <input type="hidden" name="redirect" value="%s">
    <input type="hidden" name="callback" value="%s">
    <div class="form-group">
      <label for="username">Username</label>
      <input type="text" id="username" name="username" required autofocus>
    </div>
    <div class="form-group">
      <label for="password">Password</label>
      <input type="password" id="password" name="password" required>
    </div>
    <button type="submit" class="btn">Sign in</button>
  </form>
</div>
</body>
</html>`
