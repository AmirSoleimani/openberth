package httphandler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AmirSoleimani/openberth/apps/server/internal/config"
	"github.com/AmirSoleimani/openberth/apps/server/internal/service"
	"github.com/AmirSoleimani/openberth/apps/server/internal/store"
)

// OAuthHandlers holds the OAuth endpoint handlers.
type OAuthHandlers struct {
	cfg   *config.Config
	store *store.Store
	auth  func(r *http.Request) *store.User // shared authenticator from Handlers
}

// NewOAuthHandlers creates a new OAuthHandlers instance.
// The auth parameter is typically Handlers.Authenticate.
func NewOAuthHandlers(cfg *config.Config, s *store.Store, auth func(r *http.Request) *store.User) *OAuthHandlers {
	return &OAuthHandlers{cfg: cfg, store: s, auth: auth}
}

// ── RFC 9728: Protected Resource Metadata ─────────────────────────

func (o *OAuthHandlers) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, map[string]interface{}{
		"resource":              o.cfg.BaseURL + "/mcp",
		"authorization_servers": []string{o.cfg.BaseURL},
	})
}

// ── RFC 8414: Authorization Server Metadata ───────────────────────

func (o *OAuthHandlers) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, 200, map[string]interface{}{
		"issuer":                                o.cfg.BaseURL,
		"authorization_endpoint":                o.cfg.BaseURL + "/oauth/authorize",
		"token_endpoint":                        o.cfg.BaseURL + "/oauth/token",
		"registration_endpoint":                 o.cfg.BaseURL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

// ── RFC 7591: Dynamic Client Registration ─────────────────────────

func (o *OAuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, 400, "Invalid JSON")
		return
	}
	if len(body.RedirectURIs) == 0 {
		jsonErr(w, 400, "redirect_uris required")
		return
	}

	client := &store.OAuthClient{
		ClientID:     "oac_" + service.RandomHex(16),
		RedirectURIs: body.RedirectURIs,
		ClientName:   body.ClientName,
	}

	if err := o.store.CreateOAuthClient(client); err != nil {
		jsonErr(w, 500, "Failed to register client")
		return
	}

	jsonResp(w, 201, map[string]interface{}{
		"client_id":                  client.ClientID,
		"client_name":                client.ClientName,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// ── Authorization Endpoint ────────────────────────────────────────

func (o *OAuthHandlers) Authorize(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		o.authorizeGet(w, r)
	case http.MethodPost:
		o.authorizePost(w, r)
	default:
		http.NotFound(w, r)
	}
}

// validateAuthorizeParams validates the client_id / redirect_uri / code_challenge
// triple and returns the resolved OAuth client. On failure it writes an error
// response and returns nil.
func (o *OAuthHandlers) validateAuthorizeParams(w http.ResponseWriter, clientID, redirectURI, codeChallenge, codeChallengeMethod string) *store.OAuthClient {
	if clientID == "" || redirectURI == "" || codeChallenge == "" {
		jsonErr(w, 400, "Missing required parameters: client_id, redirect_uri, code_challenge")
		return nil
	}
	if codeChallengeMethod != "" && codeChallengeMethod != "S256" {
		jsonErr(w, 400, "code_challenge_method must be S256")
		return nil
	}
	client, _ := o.store.GetOAuthClient(clientID)
	if client == nil {
		jsonErr(w, 400, "Unknown client_id")
		return nil
	}
	if !containsString(client.RedirectURIs, redirectURI) {
		jsonErr(w, 400, "redirect_uri not registered for this client")
		return nil
	}
	return client
}

// issueAuthCode mints an authorization code for the given user and redirects
// to the client's redirect_uri with ?code=... (and ?state=... if provided).
func (o *OAuthHandlers) issueAuthCode(w http.ResponseWriter, r *http.Request, userID, clientID, redirectURI, state, codeChallenge string) {
	code := "oauthcode_" + service.RandomHex(24)
	oauthCode := &store.OAuthCode{
		Code:          code,
		ClientID:      clientID,
		UserID:        userID,
		RedirectURI:   redirectURI,
		CodeChallenge: codeChallenge,
	}
	if err := o.store.CreateOAuthCode(oauthCode); err != nil {
		jsonErr(w, 500, "Internal error")
		return
	}
	redirectURL, _ := url.Parse(redirectURI)
	q := redirectURL.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	redirectURL.RawQuery = q.Encode()
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// denyAuthRequest redirects back to the client's redirect_uri with an
// access_denied error per RFC 6749 §4.1.2.1.
func denyAuthRequest(w http.ResponseWriter, r *http.Request, redirectURI, state string) {
	redirectURL, _ := url.Parse(redirectURI)
	q := redirectURL.Query()
	q.Set("error", "access_denied")
	if state != "" {
		q.Set("state", state)
	}
	redirectURL.RawQuery = q.Encode()
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

func (o *OAuthHandlers) authorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")

	if codeChallengeMethod == "" {
		codeChallengeMethod = "S256"
	}
	client := o.validateAuthorizeParams(w, clientID, redirectURI, codeChallenge, codeChallengeMethod)
	if client == nil {
		return
	}

	// Require a logged-in session. Never issue a code on GET — that is the
	// silent-grant vulnerability. The user must confirm via POST below.
	var user *store.User
	if o.auth != nil {
		user = o.auth(r)
	}
	if user == nil {
		// /login always serves regardless of cfg.WebDisabled — it's part of
		// the OAuth / OIDC flow, not the gallery. If OIDC is configured, the
		// login page surfaces the SSO button; sso_only mode auto-redirects.
		loginURL := fmt.Sprintf("/login?redirect=%s", url.QueryEscape(r.URL.String()))
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}

	renderConsentPage(w, client, redirectURI, clientID, state, codeChallenge, user)
}

func (o *OAuthHandlers) authorizePost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	action := r.FormValue("action")
	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")

	// Session-based consent flow (Allow/Deny from consent page).
	// SameSite=Lax on the session cookie prevents cross-origin POSTs from
	// carrying the cookie, so reaching this branch requires the POST to come
	// from the OpenBerth origin — i.e. the consent page the user saw.
	if action != "" {
		if o.validateAuthorizeParams(w, clientID, redirectURI, codeChallenge, "S256") == nil {
			return
		}
		var user *store.User
		if o.auth != nil {
			user = o.auth(r)
		}
		if user == nil {
			jsonErr(w, 401, "Session expired. Please log in again.")
			return
		}
		switch action {
		case "allow":
			o.issueAuthCode(w, r, user.ID, clientID, redirectURI, state, codeChallenge)
		case "deny":
			denyAuthRequest(w, r, redirectURI, state)
		default:
			jsonErr(w, 400, "Invalid action.")
		}
		return
	}

	// Legacy: user pastes their API key into the consent form (no session).
	if apiKey == "" || clientID == "" || redirectURI == "" || codeChallenge == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(400)
		fmt.Fprint(w, `<html><body><h2>Missing fields</h2><p>Please go back and fill in all fields.</p></body></html>`)
		return
	}
	if o.validateAuthorizeParams(w, clientID, redirectURI, codeChallenge, "S256") == nil {
		return
	}
	user, err := o.store.GetUserByKey(apiKey)
	if err != nil || user == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(401)
		fmt.Fprint(w, `<html><body><h2>Invalid API Key</h2><p>The API key you entered is not valid. Please try again.</p><p><a href="javascript:history.back()">Go back</a></p></body></html>`)
		return
	}
	o.issueAuthCode(w, r, user.ID, clientID, redirectURI, state, codeChallenge)
}

// renderConsentPage shows an HTML approval page with Allow/Deny buttons.
// The form POSTs back to /oauth/authorize. Everything user-influenced is
// HTML-escaped; the redirect URI is shown prominently because it is the
// most security-relevant field the user must inspect.
func renderConsentPage(w http.ResponseWriter, client *store.OAuthClient, redirectURI, clientID, state, codeChallenge string, user *store.User) {
	clientName := client.ClientName
	if strings.TrimSpace(clientName) == "" {
		clientName = "(unnamed client)"
	}
	userLabel := user.DisplayName
	if userLabel == "" {
		userLabel = user.Name
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, consentPageHTML,
		html.EscapeString(clientName),
		html.EscapeString(userLabel),
		html.EscapeString(redirectURI),
		html.EscapeString(clientID),
		html.EscapeString(redirectURI),
		html.EscapeString(state),
		html.EscapeString(codeChallenge),
	)
}

const consentPageHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenBerth - Authorize</title>
<style>` + pageStyles + `
  .consent-row { font-size: 0.875rem; margin-bottom: 0.75rem; }
  .consent-row .k { color: var(--muted-fg); display: block; margin-bottom: 0.25rem; }
  .consent-row .v { word-break: break-all; }
  .redirect-uri { background: var(--muted); border: 1px solid var(--border); border-radius: var(--radius); padding: 0.5rem 0.625rem; font-size: 0.8125rem; }
  .btn-row { display: flex; gap: 0.5rem; margin-top: 1.5rem; }
  .btn-row .btn, .btn-row .btn-outline { margin-top: 0; }
  .warn-note { font-size: 0.75rem; color: var(--muted-fg); margin-top: 0.75rem; line-height: 1.4; }
</style>
</head>
<body>
  <div class="card">
    <h1>Authorize access</h1>
    <p class="subtitle"><strong>%s</strong> is requesting access to your OpenBerth account.</p>

    <div class="consent-row">
      <span class="k">Signed in as</span>
      <span class="v">%s</span>
    </div>

    <div class="consent-row">
      <span class="k">Will redirect to</span>
      <div class="redirect-uri">%s</div>
    </div>

    <p class="warn-note">If you do not recognise this application or redirect URL, click Deny. Approving grants full API access equivalent to your own account.</p>

    <form method="POST" action="/oauth/authorize">
      <input type="hidden" name="client_id" value="%s">
      <input type="hidden" name="redirect_uri" value="%s">
      <input type="hidden" name="state" value="%s">
      <input type="hidden" name="code_challenge" value="%s">
      <div class="btn-row">
        <button class="btn-outline" type="submit" name="action" value="deny">Deny</button>
        <button class="btn" type="submit" name="action" value="allow">Allow</button>
      </div>
    </form>
  </div>
</body>
</html>`

// ── Token Endpoint ────────────────────────────────────────────────

// tokenParams holds all possible token request fields.
// Populated from either form-encoded or JSON body.
type tokenParams struct {
	GrantType    string `json:"grant_type"`
	Code         string `json:"code"`
	ClientID     string `json:"client_id"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
	RefreshToken string `json:"refresh_token"`
}

func (o *OAuthHandlers) parseTokenParams(r *http.Request) tokenParams {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var p tokenParams
		json.NewDecoder(r.Body).Decode(&p)
		return p
	}
	r.ParseForm()
	return tokenParams{
		GrantType:    r.FormValue("grant_type"),
		Code:         r.FormValue("code"),
		ClientID:     r.FormValue("client_id"),
		RedirectURI:  r.FormValue("redirect_uri"),
		CodeVerifier: r.FormValue("code_verifier"),
		RefreshToken: r.FormValue("refresh_token"),
	}
}

func (o *OAuthHandlers) Token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	p := o.parseTokenParams(r)

	switch p.GrantType {
	case "authorization_code":
		o.tokenAuthCode(w, p)
	case "refresh_token":
		o.tokenRefresh(w, p)
	default:
		jsonErr(w, 400, "Unsupported grant_type")
	}
}

func (o *OAuthHandlers) tokenAuthCode(w http.ResponseWriter, p tokenParams) {
	code := p.Code
	clientID := p.ClientID
	redirectURI := p.RedirectURI
	codeVerifier := p.CodeVerifier

	if code == "" || clientID == "" || redirectURI == "" || codeVerifier == "" {
		jsonErr(w, 400, "Missing required parameters")
		return
	}

	oauthCode, err := o.store.GetOAuthCode(code)
	if err != nil || oauthCode == nil {
		jsonErr(w, 400, "Invalid authorization code")
		return
	}
	if oauthCode.Used {
		jsonErr(w, 400, "Authorization code already used")
		return
	}
	if oauthCode.Expired {
		jsonErr(w, 400, "Authorization code expired")
		return
	}
	if oauthCode.ClientID != clientID {
		jsonErr(w, 400, "client_id mismatch")
		return
	}
	if oauthCode.RedirectURI != redirectURI {
		jsonErr(w, 400, "redirect_uri mismatch")
		return
	}

	// PKCE verification: SHA256(code_verifier) must match code_challenge
	h := sha256.Sum256([]byte(codeVerifier))
	computedChallenge := base64.RawURLEncoding.EncodeToString(h[:])
	if computedChallenge != oauthCode.CodeChallenge {
		jsonErr(w, 400, "PKCE verification failed")
		return
	}

	o.store.MarkOAuthCodeUsed(code)

	accessToken := "sat_" + service.RandomHex(32)
	refreshToken := "srt_" + service.RandomHex(32)

	o.store.CreateOAuthToken(&store.OAuthToken{
		Token:     accessToken,
		TokenType: "access",
		ClientID:  clientID,
		UserID:    oauthCode.UserID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
	o.store.CreateOAuthToken(&store.OAuthToken{
		Token:     refreshToken,
		TokenType: "refresh",
		ClientID:  clientID,
		UserID:    oauthCode.UserID,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	})

	w.Header().Set("Cache-Control", "no-store")
	jsonResp(w, 200, map[string]interface{}{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"refresh_token": refreshToken,
	})
}

func (o *OAuthHandlers) tokenRefresh(w http.ResponseWriter, p tokenParams) {
	refreshToken := p.RefreshToken
	clientID := p.ClientID

	if refreshToken == "" {
		jsonErr(w, 400, "refresh_token required")
		return
	}

	token, err := o.store.GetOAuthToken(refreshToken)
	if err != nil || token == nil {
		jsonErr(w, 400, "Invalid refresh token")
		return
	}
	if token.TokenType != "refresh" {
		jsonErr(w, 400, "Invalid refresh token")
		return
	}
	if token.Expired {
		jsonErr(w, 400, "Refresh token expired")
		return
	}
	if clientID != "" && token.ClientID != clientID {
		jsonErr(w, 400, "client_id mismatch")
		return
	}

	newAccessToken := "sat_" + service.RandomHex(32)
	o.store.CreateOAuthToken(&store.OAuthToken{
		Token:     newAccessToken,
		TokenType: "access",
		ClientID:  token.ClientID,
		UserID:    token.UserID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})

	w.Header().Set("Cache-Control", "no-store")
	jsonResp(w, 200, map[string]interface{}{
		"access_token":  newAccessToken,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"refresh_token": refreshToken,
	})
}

// ── Helpers ───────────────────────────────────────────────────────

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

