package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spsp4755/load-observatory/internal/auth"
	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/store"
)

// Once login is enabled, the browser-facing API must reject an unauthenticated
// request, but the agent (which has no browser to run a login redirect in)
// and the health check must stay reachable exactly as before.
func TestHumanAPIRequiresSessionButAgentAndHealthDoNot(t *testing.T) {
	// Config with an IssuerURL but no NewGate discovery: enough to enable the
	// gate without making a network call to a Keycloak that doesn't exist here.
	gate := auth.NewGate(auth.Config{IssuerURL: "https://keycloak.invalid", SessionKey: []byte("test-session-signing-key-32-bytes!!")})
	server := NewServer(store.NewMemoryStore()).WithAuth(gate)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("/api/runs without a session should be rejected, got %d", response.Code)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("/api/health must stay open, got %d", response.Code)
	}

	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/agent/heartbeat", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("agent endpoints must stay open, got %d", response.Code)
	}
}

// A run must record who launched it once login is enabled, so a shared
// controller can answer "who ran this test" without guessing.
func TestCreateRunRecordsTheSignedInUser(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: "http://10.0.0.10/v1/chat/completions"})
	gate := auth.NewGate(auth.Config{IssuerURL: "https://keycloak.invalid", SessionKey: []byte("test-session-signing-key-32-bytes!!")})
	server := NewServer(memory).WithAuth(gate)
	cookie, err := gate.NewSessionCookieForTesting(auth.User{Subject: "u-1", Name: "Taeji Park"})
	if err != nil {
		t.Fatalf("mint session: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/runs", bytes.NewBufferString(`{"target_id":"`+target.ID+`","mode":"vu","vus":2,"duration_seconds":1}`))
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"created_by":"Taeji Park"`) {
		t.Fatalf("response should carry created_by, got %s", response.Body.String())
	}
	runs := memory.ListRuns()
	if len(runs) != 1 || runs[0].CreatedBy != "Taeji Park" {
		t.Fatalf("the stored run should record who launched it: %+v", runs)
	}
}

// Without WithAuth, every route stays exactly as open as it always was.
func TestHumanAPIStaysOpenWhenAuthIsNotConfigured(t *testing.T) {
	server := NewServer(store.NewMemoryStore())
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("without auth configured, /api/runs should be open, got %d", response.Code)
	}
}
