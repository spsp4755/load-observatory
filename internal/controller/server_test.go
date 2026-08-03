package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/store"
)

func TestCreateRunReturnsQueuedRun(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: "http://10.0.0.10/v1/chat/completions"})
	server := NewServer(memory)
	request := httptest.NewRequest(http.MethodPost, "/api/runs", bytes.NewBufferString(`{"target_id":"`+target.ID+`","mode":"vu","vus":2,"duration_seconds":1}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); !bytes.Contains([]byte(got), []byte(`"status":"queued"`)) {
		t.Fatalf("expected queued run, got %s", got)
	}
	if got := response.Body.String(); !bytes.Contains([]byte(got), []byte(`"max_error_percent":2`)) || !bytes.Contains([]byte(got), []byte(`"max_p95_millis":2000`)) {
		t.Fatalf("threshold defaults missing: %s", got)
	}
}

func TestCreateModelTargetRequiresModelName(t *testing.T) {
	server := NewServer(store.NewMemoryStore())
	request := httptest.NewRequest(http.MethodPost, "/api/targets", bytes.NewBufferString(`{"name":"model","type":"model","url":"http://192.168.0.249:1234/v1/chat/completions"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d", response.Code)
	}
}

func TestAgentClaimAndResultCompleteRun(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	server := NewServer(memory)

	claim := httptest.NewRecorder()
	server.ServeHTTP(claim, httptest.NewRequest(http.MethodPost, "/api/agent/claim", nil))
	if claim.Code != http.StatusOK || !bytes.Contains(claim.Body.Bytes(), []byte(`"id":"`+run.ID+`"`)) {
		t.Fatalf("unexpected claim: %d %s", claim.Code, claim.Body.String())
	}

	result := httptest.NewRecorder()
	server.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/agent/runs/"+run.ID+"/result", bytes.NewBufferString(`{"successes":1}`)))
	if result.Code != http.StatusOK || !bytes.Contains(result.Body.Bytes(), []byte(`"status":"completed"`)) {
		t.Fatalf("unexpected result: %d %s", result.Code, result.Body.String())
	}
}

func TestListRunsReturnsCreatedRun(t *testing.T) {
	memory := store.NewMemoryStore()
	memory.CreateRun(core.RunConfig{TargetID: "target-1", Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"queued"`)) {
		t.Fatalf("unexpected runs: %d %s", response.Code, response.Body.String())
	}
}
