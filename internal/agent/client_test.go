package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunOnceClaimsAndReportsResult(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	reported := false
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/claim":
			fmt.Fprintf(w, `{"run":{"id":"run-1","config":{"mode":"vu","vus":1,"duration_seconds":1}},"shard":{"id":"shard-1"},"target":{"type":"web","url":%q}}`, target.URL)
		case "/api/agent/runs/run-1/shards/shard-1/result":
			reported = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	ran, err := RunOnce(context.Background(), controller.URL)
	if err != nil || !ran || !reported {
		t.Fatalf("ran=%t reported=%t err=%v", ran, reported, err)
	}
}

func TestRunOnceKeepsAgentHeartbeatDuringLongRun(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	var heartbeats atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/heartbeat":
			heartbeats.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case "/api/agent/claim":
			fmt.Fprintf(w, `{"run":{"id":"run-1","config":{"mode":"vu","vus":1,"duration_seconds":2}},"shard":{"id":"shard-1"},"target":{"type":"web","url":%q}}`, target.URL)
		case "/api/agent/runs/run-1/shards/shard-1/result":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	if ran, err := RunOnce(context.Background(), controller.URL); err != nil || !ran || heartbeats.Load() < 2 {
		t.Fatalf("ran=%t heartbeats=%d err=%v", ran, heartbeats.Load(), err)
	}
}
