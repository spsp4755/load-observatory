// Package auth signs the human-facing API in with Keycloak (or any OIDC
// provider) using the standard confidential-client authorization code flow.
// Sessions are a signed, stateless cookie rather than a server-side store, so
// the controller can run multiple replicas without sharing session state -
// the Postgres store already carries the run data that matters.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config is read from environment variables by cmd/controller. A zero-value
// Config (no IssuerURL) means auth is disabled - existing deployments and the
// test suite keep working without a Keycloak instance to talk to.
type Config struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	SessionKey   []byte // HMAC key for signing session/state cookies
}

func (c Config) enabled() bool { return c.IssuerURL != "" }

const (
	sessionCookie  = "lo_session"
	stateCookie    = "lo_oauth_state"
	sessionTTL     = 12 * time.Hour
	stateTTL       = 5 * time.Minute
	discoveryRetry = 30 * time.Second
)

// User is what a verified ID token contributes to a request: just enough to
// show who ran a test and to name a session, not a full identity record.
type User struct {
	Subject string `json:"sub"`
	Name    string `json:"name"`
}

type contextKey struct{}

// UserFromContext returns the signed-in user, if the request passed through
// Gate. When auth is disabled, no request ever carries one.
func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(contextKey{}).(User)
	return user, ok
}

// Gate wires the OIDC routes and protects everything else behind a session
// cookie. When cfg is disabled, it returns handler unchanged - the same
// unauthenticated behavior the tool always had.
type Gate struct {
	cfg Config

	mu       sync.Mutex
	provider *oidc.Provider
	oauth    oauth2.Config
}

func NewGate(cfg Config) *Gate {
	gate := &Gate{cfg: cfg}
	if cfg.enabled() {
		go gate.discoverUntilReady()
	}
	return gate
}

func (g *Gate) discoverUntilReady() {
	for {
		provider, err := oidc.NewProvider(context.Background(), g.cfg.IssuerURL)
		if err == nil {
			g.mu.Lock()
			g.provider = provider
			g.oauth = oauth2.Config{
				ClientID:     g.cfg.ClientID,
				ClientSecret: g.cfg.ClientSecret,
				RedirectURL:  g.cfg.RedirectURL,
				Endpoint:     provider.Endpoint(),
				Scopes:       []string{oidc.ScopeOpenID, "profile"},
			}
			g.mu.Unlock()
			return
		}
		log.Printf("auth: OIDC discovery against %s failed, retrying in %s: %v", g.cfg.IssuerURL, discoveryRetry, err)
		time.Sleep(discoveryRetry)
	}
}

func (g *Gate) ready() (*oidc.Provider, oauth2.Config, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.provider, g.oauth, g.provider != nil
}

// Enabled reports whether login is required at all.
func (g *Gate) Enabled() bool { return g.cfg.enabled() }

// Handle routes the three OIDC endpoints. Callers should mount it at
// /auth/login, /auth/callback and /auth/logout.
func (g *Gate) Login(w http.ResponseWriter, r *http.Request) {
	_, oauthCfg, ok := g.ready()
	if !ok {
		http.Error(w, "identity provider not reachable yet, try again shortly", http.StatusServiceUnavailable)
		return
	}
	state := randomToken()
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: state, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: int(stateTTL.Seconds())})
	http.Redirect(w, r, oauthCfg.AuthCodeURL(state), http.StatusFound)
}

func (g *Gate) Callback(w http.ResponseWriter, r *http.Request) {
	provider, oauthCfg, ok := g.ready()
	if !ok {
		http.Error(w, "identity provider not reachable yet, try again shortly", http.StatusServiceUnavailable)
		return
	}
	expected, err := r.Cookie(stateCookie)
	if err != nil || r.URL.Query().Get("state") != expected.Value {
		http.Error(w, "invalid OAuth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1})
	token, err := oauthCfg.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusBadGateway)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "provider did not return an ID token", http.StatusBadGateway)
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: g.cfg.ClientID}).Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, "ID token verification failed", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	_ = idToken.Claims(&claims)
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	if name == "" {
		name = idToken.Subject
	}
	user := User{Subject: idToken.Subject, Name: name}
	cookie, err := g.signSession(user)
	if err != nil {
		http.Error(w, "failed to start session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (g *Gate) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

// Session reports the caller's identity for the frontend to render, or 401 if
// there is none - the frontend uses this to decide whether to show the app or
// redirect to /auth/login.
func (g *Gate) Session(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(user)
}

// Require wraps a handler so it only runs for a request carrying a valid
// session, attaching the user to the request context. When auth is disabled
// it is a no-op passthrough, preserving today's open-access behavior.
func (g *Gate) Require(next http.Handler) http.Handler {
	if !g.cfg.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		user, err := g.verifySession(cookie.Value)
		if err != nil {
			http.Error(w, "session invalid or expired", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, user)))
	})
}

type sessionClaims struct {
	User
	Exp int64 `json:"exp"`
}

// NewSessionCookieForTesting mints a valid session cookie without going
// through the OIDC redirect dance, so other packages can test what happens
// behind Require without standing up a real Keycloak.
func (g *Gate) NewSessionCookieForTesting(user User) (*http.Cookie, error) {
	return g.signSession(user)
}

func (g *Gate) signSession(user User) (*http.Cookie, error) {
	payload, err := json.Marshal(sessionClaims{User: user, Exp: time.Now().Add(sessionTTL).Unix()})
	if err != nil {
		return nil, err
	}
	value, err := sign(payload, g.cfg.SessionKey)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())}, nil
}

func (g *Gate) verifySession(value string) (User, error) {
	payload, err := verify(value, g.cfg.SessionKey)
	if err != nil {
		return User{}, err
	}
	var claims sessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return User{}, err
	}
	if time.Now().Unix() > claims.Exp {
		return User{}, errors.New("session expired")
	}
	return claims.User, nil
}

// sign/verify implement a minimal signed-cookie format (payload.signature,
// both base64url) rather than pulling in a session-store dependency: sessions
// here are small, stateless, and need no server-side revocation list.
func sign(payload []byte, key []byte) (string, error) {
	if len(key) == 0 {
		return "", errors.New("no session signing key configured")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verify(value string, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("no session signing key configured")
	}
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return nil, errors.New("malformed session cookie")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("session signature mismatch")
	}
	return payload, nil
}

func randomToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}
