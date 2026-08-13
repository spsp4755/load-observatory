package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
	"github.com/spsp4755/load-observatory/internal/store"
)

func TestCaptureProxyForwardsStreamWithoutPersistingContentOrClientToken(t *testing.T) {
	var upstreamAuthorization, upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuthorization = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		upstreamBody = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"secret answer\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":123,\"completion_tokens\":45}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: upstream.URL, Model: "qwen", APIKey: "upstream-secret"})
	server := NewServer(memory).WithCaptureProxy("capture-secret")
	body := `{"model":"qwen","messages":[{"role":"user","content":"private source code"}],"max_tokens":4096,"stream":true}`
	request := httptest.NewRequest(http.MethodPost, "/capture/"+target.ID+"/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer capture-secret")
	request.Header.Set("X-Load-Observatory-Session", "jupyter-user-17-job-4")
	request.Header.Set("X-Load-Observatory-Client", "Qwen Code")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "secret answer") {
		t.Fatalf("proxy response = %d %q", response.Code, response.Body.String())
	}
	if upstreamAuthorization != "Bearer upstream-secret" {
		t.Fatalf("upstream authorization = %q", upstreamAuthorization)
	}
	if !strings.Contains(upstreamBody, "private source code") {
		t.Fatal("request body was not forwarded")
	}
	captures := memory.ListCaptures()
	if len(captures) != 1 || len(captures[0].Events) != 1 {
		t.Fatalf("captures = %#v", captures)
	}
	event := captures[0].Events[0]
	if event.PromptTokens != 123 || event.OutputTokens != 45 || event.MaxTokens != 4096 || event.FinishReason != "stop" {
		t.Fatalf("event = %#v", event)
	}
	if event.TimestampMillis != 0 {
		t.Fatalf("first event timestamp = %d, want 0", event.TimestampMillis)
	}
	persisted, _ := json.Marshal(captures)
	for _, forbidden := range []string{"private source code", "secret answer", "capture-secret", "upstream-secret", "jupyter-user-17-job-4"} {
		if strings.Contains(string(persisted), forbidden) {
			t.Fatalf("capture leaked %q: %s", forbidden, persisted)
		}
	}
}

func TestCaptureProxyRejectsMissingTokenAndSession(t *testing.T) {
	memory := store.NewMemoryStore()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: "http://127.0.0.1:1", Model: "qwen"})
	server := NewServer(memory).WithCaptureProxy("capture-secret")
	path := "/capture/" + target.ID + "/v1/chat/completions"

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", response.Code)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"messages":[]}`))
	request.Header.Set("Authorization", "Bearer capture-secret")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing session status = %d", response.Code)
	}
}

func TestCaptureSettingsCanBeManagedWithoutReadingTokenBack(t *testing.T) {
	memory := store.NewMemoryStore()
	server := NewServer(memory)
	body := `{"enabled":true,"token":"a-very-long-ui-managed-capture-token","session_idle_minutes":20,"max_events_per_session":64,"retention_sessions":80,"default_replay_vus":40,"replay_think_time_scale":1.5,"replay_buffer_seconds":420,"replay_drain_seconds":180}`
	request := httptest.NewRequest(http.MethodPut, "/api/capture-settings", strings.NewReader(body))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update settings = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "a-very-long-ui-managed") || strings.Contains(response.Body.String(), "token_hash") {
		t.Fatalf("response exposed token material: %s", response.Body.String())
	}
	var settings map[string]any
	if json.Unmarshal(response.Body.Bytes(), &settings) != nil || settings["token_configured"] != true || settings["default_replay_vus"] != float64(40) {
		t.Fatalf("settings = %#v", settings)
	}
	stored := memory.GetCaptureSettings()
	if stored.TokenHash == "" || strings.Contains(stored.TokenHash, "ui-managed") {
		t.Fatalf("token was not hashed: %#v", stored)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	target := memory.CreateTarget(core.Target{Name: "model", Type: core.TargetTypeModel, URL: upstream.URL, Model: "qwen"})
	proxyRequest := httptest.NewRequest(http.MethodPost, "/capture/"+target.ID+"/v1/chat/completions", strings.NewReader(`{"messages":[],"max_tokens":8}`))
	proxyRequest.Header.Set("Authorization", "Bearer a-very-long-ui-managed-capture-token")
	proxyRequest.Header.Set("X-Load-Observatory-Session", "ui-session")
	proxyResponse := httptest.NewRecorder()
	server.ServeHTTP(proxyResponse, proxyRequest)
	if proxyResponse.Code != http.StatusOK {
		t.Fatalf("UI token did not enable proxy: %d %s", proxyResponse.Code, proxyResponse.Body.String())
	}
}
