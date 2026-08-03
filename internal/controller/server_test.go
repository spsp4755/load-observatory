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

func TestSearchSchedulesNextStepAfterStableResult(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	server := NewServer(memory)
	create := httptest.NewRecorder()
	server.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/searches", bytes.NewBufferString(`{"run":{"target_id":"`+target.ID+`","mode":"vu","duration_seconds":1,"max_tokens":32,"max_error_percent":2,"max_p95_millis":2000},"start_load":5,"max_load":10}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create search: %d %s", create.Code, create.Body.String())
	}
	claim := httptest.NewRecorder()
	server.ServeHTTP(claim, httptest.NewRequest(http.MethodPost, "/api/agent/claim", nil))
	if claim.Code != http.StatusOK || !bytes.Contains(claim.Body.Bytes(), []byte(`"vus":5`)) {
		t.Fatalf("claim: %d %s", claim.Code, claim.Body.String())
	}
	result := httptest.NewRecorder()
	server.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/agent/runs/run-3/result", bytes.NewBufferString(`{"total":10,"successes":10,"latency":{"p95_millis":100}}`)))
	if result.Code != http.StatusOK {
		t.Fatalf("result: %d", result.Code)
	}
	searches := httptest.NewRecorder()
	server.ServeHTTP(searches, httptest.NewRequest(http.MethodGet, "/api/searches", nil))
	if searches.Code != http.StatusOK || !bytes.Contains(searches.Body.Bytes(), []byte(`"next_load":10`)) {
		t.Fatalf("search: %d %s", searches.Code, searches.Body.String())
	}
}

func TestCancelSearchCancelsQueuedStep(t *testing.T) {
	memory := store.NewMemoryStore(); target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"}); server := NewServer(memory)
	create := httptest.NewRecorder(); server.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/searches", bytes.NewBufferString(`{"run":{"target_id":"`+target.ID+`","mode":"vu","duration_seconds":1,"max_tokens":32},"start_load":5,"max_load":10}`)))
	cancel := httptest.NewRecorder(); server.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/searches/search-2/cancel", nil))
	if cancel.Code != http.StatusOK || !bytes.Contains(cancel.Body.Bytes(), []byte(`"status":"cancelled"`)) { t.Fatalf("cancel: %d %s", cancel.Code, cancel.Body.String()) }
}
