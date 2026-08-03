package monitor

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSampleReadsPrometheusValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"value":[0,"42.5"]}]}}`))
	}))
	defer server.Close()
	sample := New(server.URL).Sample()
	if sample.Status != "collected" || sample.GPUUtilization != 42.5 {
		t.Fatalf("sample: %+v", sample)
	}
}
