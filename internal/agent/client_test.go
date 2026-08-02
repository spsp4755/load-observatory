package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

)

func TestRunOnceClaimsAndReportsResult(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	reported := false
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/claim":
			fmt.Fprintf(w, `{"run":{"id":"run-1","config":{"mode":"vu","vus":1,"duration_seconds":1}},"target":{"type":"web","url":%q}}`, target.URL)
		case "/api/agent/runs/run-1/result":
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
