package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spsp4755/load-observatory/internal/auth"
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

// Without WithAuth, every route stays exactly as open as it always was.
func TestHumanAPIStaysOpenWhenAuthIsNotConfigured(t *testing.T) {
	server := NewServer(store.NewMemoryStore())
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("without auth configured, /api/runs should be open, got %d", response.Code)
	}
}
