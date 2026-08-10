package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var testKey = []byte("test-session-signing-key-32-bytes!!")

// A session cookie is the only thing standing between an open API and a
// gated one, so it must round-trip exactly what was signed and reject
// anything that was not signed with this server's key.
func TestSignVerifyRoundTrip(t *testing.T) {
	gate := &Gate{cfg: Config{IssuerURL: "https://keycloak.internal", SessionKey: testKey}}
	cookie, err := gate.signSession(User{Subject: "u-1", Name: "Taeji"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	user, err := gate.verifySession(cookie.Value)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if user.Subject != "u-1" || user.Name != "Taeji" {
		t.Fatalf("round-trip changed the user: %+v", user)
	}
}

func TestVerifyRejectsATamperedCookie(t *testing.T) {
	gate := &Gate{cfg: Config{IssuerURL: "https://keycloak.internal", SessionKey: testKey}}
	cookie, err := gate.signSession(User{Subject: "u-1", Name: "Taeji"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := cookie.Value[:len(cookie.Value)-1] + "x"
	if _, err := gate.verifySession(tampered); err == nil {
		t.Fatal("a modified signature must not verify")
	}
}

func TestVerifyRejectsAnExpiredSession(t *testing.T) {
	gate := &Gate{cfg: Config{IssuerURL: "https://keycloak.internal", SessionKey: testKey}}
	payload, err := json.Marshal(sessionClaims{User: User{Subject: "u-1"}, Exp: time.Now().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	value, err := sign(payload, testKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := gate.verifySession(value); err == nil {
		t.Fatal("an expired session must not verify")
	}
}

// With no OIDC issuer configured, Require must be a pure passthrough - every
// deployment and test that predates login should keep working unauthenticated.
func TestRequirePassesThroughWhenAuthIsDisabled(t *testing.T) {
	gate := &Gate{}
	handler := gate.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("disabled auth should not block requests, got %d", recorder.Code)
	}
}

func TestRequireRejectsARequestWithNoSession(t *testing.T) {
	gate := &Gate{cfg: Config{IssuerURL: "https://keycloak.internal", SessionKey: testKey}}
	handler := gate.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("a request with no session cookie must be rejected, got %d", recorder.Code)
	}
}

func TestRequireAcceptsAValidSessionAndExposesTheUser(t *testing.T) {
	gate := &Gate{cfg: Config{IssuerURL: "https://keycloak.internal", SessionKey: testKey}}
	cookie, err := gate.signSession(User{Subject: "u-1", Name: "Taeji"})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	var seen User
	handler := gate.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("a valid session must be accepted, got %d", recorder.Code)
	}
	if seen.Subject != "u-1" {
		t.Fatalf("the handler should see the signed-in user, got %+v", seen)
	}
}
