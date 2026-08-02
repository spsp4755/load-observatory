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
}
