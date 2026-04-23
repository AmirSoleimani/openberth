package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SSO tokens authenticate a browser request to a specific tenant subdomain
// without the UI's session cookie ever being scoped to the tenant host.
//
// Wire format: "<payload>.<sig>" where
//   payload = "<userID>:<exp-unix>" (URL-safe base64, no padding)
//   sig     = HMAC-SHA256(deriveKey(master, subdomain), payload) (URL-safe
//             base64, no padding)
//
// The subdomain is mixed into the HMAC key derivation rather than the
// payload: a forged token for tenant A cannot be replayed against tenant B
// because B verifies with a different key.

// ErrSSOTokenInvalid is returned for any structural, cryptographic, or
// freshness failure during verification. The caller should treat all
// flavors the same — redirect back through the SSO handoff.
var ErrSSOTokenInvalid = errors.New("sso token invalid or expired")

// SSOTokenTTL is the default lifetime of a minted SSO token. Tokens are
// short-lived because they're bearer credentials scoped to one tenant
// subdomain; the user can always get a fresh one from the UI host as
// long as their main session is still valid.
const SSOTokenTTL = 24 * time.Hour

// MintSSOToken creates a signed handoff token for (subdomain, userID).
// The returned string is safe to embed in a URL or cookie value.
func MintSSOToken(masterKey [32]byte, subdomain, userID string, ttl time.Duration) string {
	if ttl <= 0 {
		ttl = SSOTokenTTL
	}
	exp := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%s:%d", userID, exp)
	sig := ssoSign(masterKey, subdomain, payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) +
		"." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// VerifySSOToken returns the userID encoded in token if the signature
// matches and the expiry is still in the future; otherwise returns
// ErrSSOTokenInvalid.
func VerifySSOToken(masterKey [32]byte, subdomain, token string) (string, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", ErrSSOTokenInvalid
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrSSOTokenInvalid
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrSSOTokenInvalid
	}
	expected := ssoSign(masterKey, subdomain, string(payloadBytes))
	if !hmac.Equal(sigBytes, expected) {
		return "", ErrSSOTokenInvalid
	}

	payloadStr := string(payloadBytes)
	colon := strings.LastIndexByte(payloadStr, ':')
	if colon <= 0 {
		return "", ErrSSOTokenInvalid
	}
	userID := payloadStr[:colon]
	expUnix, err := strconv.ParseInt(payloadStr[colon+1:], 10, 64)
	if err != nil {
		return "", ErrSSOTokenInvalid
	}
	if time.Now().Unix() > expUnix {
		return "", ErrSSOTokenInvalid
	}
	return userID, nil
}

// ssoSign derives a per-subdomain HMAC key from the master key and
// computes the signature. Key-binding the subdomain means a token
// issued for one tenant can't be replayed against another.
func ssoSign(masterKey [32]byte, subdomain, payload string) []byte {
	keyMac := hmac.New(sha256.New, masterKey[:])
	keyMac.Write([]byte("openberth.sso.v1:" + subdomain))
	derived := keyMac.Sum(nil)

	sigMac := hmac.New(sha256.New, derived)
	sigMac.Write([]byte(payload))
	return sigMac.Sum(nil)
}
