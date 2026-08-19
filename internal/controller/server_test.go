package controller

import (
	"bytes"
	"encoding/json"
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

func TestCreateTargetAcceptsConfiguredGatewayDomain(t *testing.T) {
	server := NewServer(store.NewMemoryStore()).WithTargetAllowedHostSuffixes(".internal,.kubagents-ofc.koreacb.com")
	request := httptest.NewRequest(http.MethodPost, "/api/targets", bytes.NewBufferString(`{"name":"gateway","type":"model","url":"https://proxy-gateway.kubagents-ofc.koreacb.com/v1/chat/completions","model":"qwen"}`))
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("configured gateway rejected: status=%d body=%s", response.Code, response.Body.String())
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
	server.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/agent/runs/"+run.ID+"/shards/shard-3/result", bytes.NewBufferString(`{"successes":1}`)))
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
	server.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/searches", bytes.NewBufferString(`{"run":{"target_id":"`+target.ID+`","mode":"vu","duration_seconds":1,"max_tokens":32,"max_error_percent":2,"max_p95_millis":2000,"shards":1},"start_load":5,"max_load":10}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create search: %d %s", create.Code, create.Body.String())
	}
	claim := httptest.NewRecorder()
	server.ServeHTTP(claim, httptest.NewRequest(http.MethodPost, "/api/agent/claim", nil))
	if claim.Code != http.StatusOK || !bytes.Contains(claim.Body.Bytes(), []byte(`"vus":5`)) || !bytes.Contains(claim.Body.Bytes(), []byte(`"search_id":"search-2"`)) {
		t.Fatalf("claim: %d %s", claim.Code, claim.Body.String())
	}
	result := httptest.NewRecorder()
	server.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/agent/runs/run-3/shards/shard-4/result", bytes.NewBufferString(`{"total":10,"successes":10,"latency":{"p95_millis":100}}`)))
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
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	server := NewServer(memory)
	create := httptest.NewRecorder()
	server.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/searches", bytes.NewBufferString(`{"run":{"target_id":"`+target.ID+`","mode":"vu","duration_seconds":1,"max_tokens":32},"start_load":5,"max_load":10}`)))
	cancel := httptest.NewRecorder()
	server.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/searches/search-2/cancel", nil))
	if cancel.Code != http.StatusOK || !bytes.Contains(cancel.Body.Bytes(), []byte(`"status":"cancelled"`)) {
		t.Fatalf("cancel: %d %s", cancel.Code, cancel.Body.String())
	}
	claim := httptest.NewRecorder()
	server.ServeHTTP(claim, httptest.NewRequest(http.MethodPost, "/api/agent/claim", nil))
	if claim.Code != http.StatusNoContent {
		t.Fatalf("cancelled search was claimed: %d %s", claim.Code, claim.Body.String())
	}
}

func TestCancelSearchCancelsRunningStep(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	server := NewServer(memory)
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/searches", bytes.NewBufferString(`{"run":{"target_id":"`+target.ID+`","mode":"vu","duration_seconds":1,"max_tokens":32},"start_load":5,"max_load":10}`)))
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/agent/claim", nil))
	cancel := httptest.NewRecorder()
	server.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/searches/search-2/cancel", nil))
	run := httptest.NewRecorder()
	server.ServeHTTP(run, httptest.NewRequest(http.MethodGet, "/api/runs/run-3", nil))
	if !bytes.Contains(run.Body.Bytes(), []byte(`"status":"cancelled"`)) {
		t.Fatalf("run not cancelled: %s", run.Body.String())
	}
}

func TestCancelRunCancelsOnlyTheRequestedRun(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "web", Type: core.TargetTypeWeb, URL: "http://10.0.0.10/health"})
	run := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	other := memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	server := NewServer(memory)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/cancel", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"cancelled"`)) {
		t.Fatalf("cancel: %d %s", response.Code, response.Body.String())
	}
	if got, _ := memory.GetRun(other.ID); got.Status != "queued" {
		t.Fatalf("unrelated run changed: %+v", got)
	}
}

func TestListTargetsRedactsAPIKey(t *testing.T) {
	server := NewServer(store.NewMemoryStore())
	server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/targets", bytes.NewBufferString(`{"name":"saved","type":"model","url":"http://10.0.0.1:8000/v1/chat/completions","model":"qwen","api_key":"secret"}`)))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/targets", nil))
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("secret")) || !bytes.Contains(response.Body.Bytes(), []byte(`"has_api_key":true`)) {
		t.Fatalf("API key leaked in target list: %d %s", response.Code, response.Body.String())
	}
}

func TestDeleteTargetReferencedByActiveRunReturnsConflict(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: "http://10.0.0.10/v1/chat/completions", Model: "model"})
	memory.CreateRun(core.RunConfig{TargetID: target.ID, Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1})
	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/targets/"+target.ID, nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
}

func TestCheckTargetUsesSavedModelAndAPIKey(t *testing.T) {
	checked := false
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		checked = payload.Model == "qwen" && payload.MaxTokens == 1 && r.Header.Get("Authorization") == "Bearer saved-key"
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: targetServer.URL, Model: "qwen", APIKey: "saved-key"})
	response := httptest.NewRecorder()
	NewServer(memory).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/targets/"+target.ID+"/check", nil))
	if response.Code != http.StatusOK || !checked || !bytes.Contains(response.Body.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("check: %d %s checked=%t", response.Code, response.Body.String(), checked)
	}
}
