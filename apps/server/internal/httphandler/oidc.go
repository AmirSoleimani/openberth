package httphandler

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/openberth/openberth/apps/server/internal/service"
	"github.com/openberth/openberth/apps/server/internal/store"
)

type oidcConfig struct {
	provider  *oidc.Provider
	oauth2Cfg oauth2.Config
	verifier  *oidc.IDTokenVerifier
}

func (h *Handlers) loadOIDCConfig() (*oidcConfig, error) {
	issuer, _ := h.svc.Store.GetSetting("oidc.issuer")
	clientID, _ := h.svc.Store.GetSetting("oidc.client_id")
	clientSecret, _ := h.svc.Store.GetSetting("oidc.client_secret")

	if issuer == "" || clientID == "" {
		return nil, nil
	}

	ctx := context.Background()
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}

	callbackURL := h.svc.Cfg.BaseURL + "/auth/oidc/callback"

	return &oidcConfig{
		provider: provider,
		oauth2Cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  callbackURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

// OIDCStart initiates the OIDC login flow.
func (h *Handlers) OIDCStart(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.loadOIDCConfig()
	if err != nil {
		log.Printf("[oidc] Failed to load config: %v", err)
		jsonErr(w, 500, "OIDC configuration error.")
		return
	}
	if cfg == nil {
		jsonErr(w, 400, "OIDC/SSO is not configured.")
		return
	}

	state := service.RandomHex(16)
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	// Store redirect/callback params for after OIDC callback
	params := r.URL.Query().Get("redirect") + "|" + r.URL.Query().Get("callback")
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_params",
		Value:    params,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})

	authURL := cfg.oauth2Cfg.AuthCodeURL(state)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// OIDCCallback handles the OIDC provider callback.
func (h *Handlers) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.loadOIDCConfig()
	if err != nil || cfg == nil {
		jsonErr(w, 500, "OIDC configuration error.")
		return
	}

	// Verify state
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		jsonErr(w, 400, "Invalid OIDC state.")
		return
	}

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{Name: "oidc_state", Path: "/", MaxAge: -1})

	// Exchange code for tokens
	ctx := context.Background()
	token, err := cfg.oauth2Cfg.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("[oidc] Token exchange failed: %v", err)
		jsonErr(w, 400, "Failed to exchange OIDC code.")
		return
	}

	// Verify ID token
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		jsonErr(w, 400, "No id_token in OIDC response.")
		return
	}
	idToken, err := cfg.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		log.Printf("[oidc] ID token verification failed: %v", err)
		jsonErr(w, 400, "Failed to verify OIDC token.")
		return
	}

	// Extract claims
	var claims struct {
		Email    string `json:"email"`
		Username string `json:"preferred_username"`
		Name     string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		jsonErr(w, 500, "Failed to parse OIDC claims.")
		return
	}

	// Get redirect/callback params cookie (needed for error redirects)
	paramsCookie, _ := r.Cookie("oidc_params")

	// Validate email domain restriction
	allowedDomains, _ := h.svc.Store.GetSetting("oidc.allowed_domains")
	if allowedDomains != "" {
		email := claims.Email
		if email == "" {
			redirectToLoginWithError(w, r, paramsCookie, "Your account has no email address.")
			return
		}
		parts := strings.SplitN(email, "@", 2)
		if len(parts) != 2 {
			redirectToLoginWithError(w, r, paramsCookie, "Invalid email format.")
			return
		}
		domain := strings.ToLower(parts[1])
		allowed := false
		for _, d := range strings.Split(allowedDomains, ",") {
			if strings.TrimSpace(strings.ToLower(d)) == domain {
				allowed = true
				break
			}
		}
		if !allowed {
			log.Printf("[oidc] Rejected login from domain '%s' (user: %s)", domain, email)
			redirectToLoginWithError(w, r, paramsCookie, "Your email domain is not allowed.")
			return
		}
	}

	// Determine username (prefer email, fallback to preferred_username)
	username := claims.Email
	if username == "" {
		username = claims.Username
	}
	if username == "" {
		jsonErr(w, 400, "OIDC provider did not return email or username.")
		return
	}

	displayName := claims.Name
	if displayName == "" {
		displayName = username
	}

	// Find or create user
	user := h.findOrCreateOIDCUser(username, displayName)
	if user == nil {
		jsonErr(w, 500, "Failed to create user account.")
		return
	}

	// Create session
	h.createSession(w, user.ID)
	log.Printf("[oidc] User '%s' logged in via SSO", username)

	// Clear params cookie (already read above)
	http.SetCookie(w, &http.Cookie{Name: "oidc_params", Path: "/", MaxAge: -1})

	redirect := ""
	callback := ""
	if paramsCookie != nil {
		parts := strings.SplitN(paramsCookie.Value, "|", 2)
		if len(parts) == 2 {
			redirect = parts[0]
			callback = parts[1]
		}
	}

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

func redirectToLoginWithError(w http.ResponseWriter, r *http.Request, paramsCookie *http.Cookie, errMsg string) {
	http.SetCookie(w, &http.Cookie{Name: "oidc_params", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "oidc_state", Path: "/", MaxAge: -1})
	target := "/login?error=" + errMsg
	if paramsCookie != nil {
		parts := strings.SplitN(paramsCookie.Value, "|", 2)
		if len(parts) == 2 {
			if parts[0] != "" {
				target += "&redirect=" + parts[0]
			}
			if parts[1] != "" {
				target += "&callback=" + parts[1]
			}
		}
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (h *Handlers) findOrCreateOIDCUser(username, displayName string) *store.User {
	user, _ := h.svc.Store.GetUserByName(username)
	if user != nil {
		return user
	}

	newUser := &store.User{
		ID:              "usr_" + service.RandomHex(8),
		Name:            username,
		APIKey:          "sc_" + service.RandomHex(24),
		Role:            "user",
		MaxDeployments:  h.svc.Cfg.DefaultMaxDeploy,
		DefaultTTLHours: h.svc.Cfg.DefaultTTLHours,
		DisplayName:     displayName,
	}

	if err := h.svc.Store.CreateUser(newUser); err != nil {
		log.Printf("[oidc] Failed to create user '%s': %v", username, err)
		return nil
	}

	log.Printf("[oidc] Auto-registered user '%s' via SSO", username)
	return newUser
}
