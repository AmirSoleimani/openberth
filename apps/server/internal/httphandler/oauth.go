package httphandler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openberth/openberth/apps/server/internal/config"
	"github.com/openberth/openberth/apps/server/internal/service"
	"github.com/openberth/openberth/apps/server/internal/store"
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

func (o *OAuthHandlers) authorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")

	if clientID == "" || redirectURI == "" || codeChallenge == "" {
		jsonErr(w, 400, "Missing required parameters: client_id, redirect_uri, code_challenge")
		return
	}
	if codeChallengeMethod != "S256" {
		jsonErr(w, 400, "code_challenge_method must be S256")
		return
	}

	client, _ := o.store.GetOAuthClient(clientID)
	if client == nil {
		jsonErr(w, 400, "Unknown client_id")
		return
	}

	if !containsString(client.RedirectURIs, redirectURI) {
		jsonErr(w, 400, "redirect_uri not registered for this client")
		return
	}

	// If user has a valid session, auto-approve (no API key paste needed)
	if o.auth != nil {
		if user := o.auth(r); user != nil {
			code := "oauthcode_" + service.RandomHex(24)
			oauthCode := &store.OAuthCode{
				Code:          code,
				ClientID:      clientID,
				UserID:        user.ID,
				RedirectURI:   redirectURI,
				CodeChallenge: codeChallenge,
			}
			if err := o.store.CreateOAuthCode(oauthCode); err != nil {
				jsonErr(w, 500, "Internal error")
				return
			}
			redirectURL, _ := url.Parse(redirectURI)
			rq := redirectURL.Query()
			rq.Set("code", code)
			if state != "" {
				rq.Set("state", state)
			}
			redirectURL.RawQuery = rq.Encode()
			http.Redirect(w, r, redirectURL.String(), http.StatusFound)
			return
		}
	}

	// No session -- redirect to login page, which will return here after login
	loginURL := fmt.Sprintf("/login?redirect=%s", url.QueryEscape(r.URL.String()))
	http.Redirect(w, r, loginURL, http.StatusFound)
}

func (o *OAuthHandlers) authorizePost(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")

	if apiKey == "" || clientID == "" || redirectURI == "" || codeChallenge == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(400)
		fmt.Fprint(w, `<html><body><h2>Missing fields</h2><p>Please go back and fill in all fields.</p></body></html>`)
		return
	}

	user, err := o.store.GetUserByKey(apiKey)
	if err != nil || user == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(401)
		fmt.Fprint(w, `<html><body><h2>Invalid API Key</h2><p>The API key you entered is not valid. Please try again.</p><p><a href="javascript:history.back()">Go back</a></p></body></html>`)
		return
	}

	code := "oauthcode_" + service.RandomHex(24)
	oauthCode := &store.OAuthCode{
		Code:          code,
		ClientID:      clientID,
		UserID:        user.ID,
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

// ── HTML Template ─────────────────────────────────────────────────

const authorizePage = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenBerth - Authorize</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #f5f5f5; display: flex; justify-content: center; align-items: center; min-height: 100vh; padding: 20px; }
  .card { background: white; border-radius: 12px; padding: 40px; max-width: 420px; width: 100%%; box-shadow: 0 2px 12px rgba(0,0,0,0.1); }
  h1 { font-size: 24px; margin-bottom: 8px; }
  .subtitle { color: #666; margin-bottom: 24px; }
  label { display: block; font-weight: 600; margin-bottom: 6px; font-size: 14px; }
  input[type=text], input[type=password] { width: 100%%; padding: 10px 12px; border: 1px solid #ddd; border-radius: 8px; font-size: 16px; margin-bottom: 20px; }
  input:focus { outline: none; border-color: #4a90d9; box-shadow: 0 0 0 3px rgba(74,144,217,0.15); }
  button { width: 100%%; padding: 12px; background: #4a90d9; color: white; border: none; border-radius: 8px; font-size: 16px; font-weight: 600; cursor: pointer; }
  button:hover { background: #3a7bc8; }
</style>
</head>
<body>
<div class="card">
  <h1>Authorize %s</h1>
  <p class="subtitle">Enter your OpenBerth API key to connect.</p>
  <form method="POST" action="/oauth/authorize">
    <input type="hidden" name="client_id" value="%s">
    <input type="hidden" name="redirect_uri" value="%s">
    <input type="hidden" name="state" value="%s">
    <input type="hidden" name="code_challenge" value="%s">
    <label for="api_key">API Key</label>
    <input type="password" id="api_key" name="api_key" placeholder="sc_..." required autofocus>
    <button type="submit">Authorize</button>
  </form>
</div>
</body>
</html>`
