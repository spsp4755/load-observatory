package monitor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
)

// fakePrometheus answers only the expressions it is given, so a test can model a
// deployment where some metrics simply do not exist.
type fakePrometheus struct {
	mu       sync.Mutex
	values   map[string]string
	queries  []string
	requests int
}

func newFakePrometheus(values map[string]string) (*fakePrometheus, *httptest.Server) {
	fake := &fakePrometheus{values: values}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expression, _ := url.QueryUnescape(r.URL.Query().Get("query"))
		fake.mu.Lock()
		fake.queries = append(fake.queries, expression)
		fake.requests++
		value, ok := fake.values[expression]
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_, _ = w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":[{"value":[0,%q]}]}}`, value)
	}))
	return fake, server
}

func (f *fakePrometheus) asked(substring string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, query := range f.queries {
		if strings.Contains(query, substring) {
			return true
		}
	}
	return false
}

func TestSampleCollectsVLLMQueueAndKVMetrics(t *testing.T) {
	_, server := newFakePrometheus(map[string]string{
		"sum(vllm:num_requests_running)":            "12",
		"sum(vllm:num_requests_waiting)":            "3",
		"avg(vllm:kv_cache_usage_perc)":             "0.87",
		"sum(rate(vllm:num_preemptions_total[1m]))": "0.5",
		"avg(DCGM_FI_PROF_DRAM_ACTIVE)":             "0.72",
	})
	defer server.Close()

	sample := New(server.URL).Sample()
	if sample.Status != "collected" {
		t.Fatalf("status %q: %s", sample.Status, sample.Message)
	}
	if sample.Backend != "vllm" {
		t.Fatalf("backend not detected: %q", sample.Backend)
	}
	for key, want := range map[string]float64{
		core.MetricRequestsRunning: 12,
		core.MetricRequestsWaiting: 3,
		core.MetricKVCacheUsage:    0.87,
		core.MetricPreemptionRate:  0.5,
		core.MetricDRAMActive:      0.72,
	} {
		got, ok := sample.Value(key)
		if !ok || got != want {
			t.Fatalf("%s: got %v (present=%t), want %v", key, got, ok, want)
		}
	}
	// A metric nothing answered for must be absent, never zero.
	if _, ok := sample.Value(core.MetricSMOccupancy); ok {
		t.Fatal("SM occupancy was never exported but appears in the sample")
	}
}

// vllm:gpu_cache_usage_perc was renamed to vllm:kv_cache_usage_perc, so both
// names have to be probed.
func TestSampleFallsBackToTheOlderVLLMKVCacheMetricName(t *testing.T) {
	fake, server := newFakePrometheus(map[string]string{
		"sum(vllm:num_requests_waiting)": "1",
		"avg(vllm:gpu_cache_usage_perc)": "0.44",
	})
	defer server.Close()

	sample := New(server.URL).Sample()
	if value, ok := sample.Value(core.MetricKVCacheUsage); !ok || value != 0.44 {
		t.Fatalf("older KV cache metric name not used: got %v present=%t", value, ok)
	}
	if !fake.asked("vllm:kv_cache_usage_perc") {
		t.Fatal("the current metric name was never tried")
	}
}

// SGLang changed its separator from "sglang:" to "sglang_" in a recent version.
func TestSampleHandlesBothSGLangSeparators(t *testing.T) {
	for _, expression := range []string{"sum(sglang:num_queue_reqs)", "sum(sglang_num_queue_reqs)"} {
		_, server := newFakePrometheus(map[string]string{expression: "7"})
		sample := New(server.URL).Sample()
		server.Close()
		value, ok := sample.Value(core.MetricRequestsWaiting)
		if !ok || value != 7 {
			t.Fatalf("%s not resolved: got %v present=%t", expression, value, ok)
		}
		if sample.Backend != "sglang" {
			t.Fatalf("%s did not identify sglang: %q", expression, sample.Backend)
		}
	}
}

func TestSampleDetectsTGI(t *testing.T) {
	_, server := newFakePrometheus(map[string]string{"sum(tgi_queue_size)": "4", "sum(tgi_batch_current_size)": "9"})
	defer server.Close()
	sample := New(server.URL).Sample()
	if sample.Backend != "tgi" {
		t.Fatalf("backend %q", sample.Backend)
	}
	if value, _ := sample.Value(core.MetricRequestsWaiting); value != 4 {
		t.Fatalf("tgi queue size not read: %v", value)
	}
}

// Missing DCGM profiling fields must degrade the sample, not silently pass.
func TestSampleReportsMissingDCGMProfilingFields(t *testing.T) {
	_, server := newFakePrometheus(map[string]string{
		"sum(vllm:num_requests_waiting)": "0",
		"avg(DCGM_FI_DEV_GPU_UTIL)":      "99",
	})
	defer server.Close()
	sample := New(server.URL).Sample()
	if sample.Status != "partial" {
		t.Fatalf("expected partial status, got %q", sample.Status)
	}
	if !strings.Contains(sample.Message, "DCGM_FI_PROF_") {
		t.Fatalf("message does not name the missing profiling fields: %q", sample.Message)
	}
}

// Without engine queue metrics the saturation verdict is impossible, and the
// sample has to say so rather than look healthy.
func TestSampleReportsMissingEngineQueueMetrics(t *testing.T) {
	_, server := newFakePrometheus(map[string]string{"avg(DCGM_FI_DEV_GPU_UTIL)": "99", "avg(DCGM_FI_PROF_DRAM_ACTIVE)": "0.5"})
	defer server.Close()
	sample := New(server.URL).Sample()
	if sample.Status != "partial" || !strings.Contains(sample.Message, "큐 지표") {
		t.Fatalf("missing queue metrics not reported: %q / %q", sample.Status, sample.Message)
	}
}

func TestSampleIsUnavailableWithoutPrometheus(t *testing.T) {
	if sample := New("").Sample(); sample.Status != "unavailable" {
		t.Fatalf("status %q", sample.Status)
	}
	_, server := newFakePrometheus(map[string]string{})
	defer server.Close()
	if sample := New(server.URL).Sample(); sample.Status != "unavailable" {
		t.Fatalf("a Prometheus that knows nothing should be unavailable, got %q", sample.Status)
	}
}

// NaN is what Prometheus renders for an empty histogram_quantile or a 0/0 ratio,
// and it must not become a metric value.
func TestNaNIsTreatedAsAbsent(t *testing.T) {
	_, server := newFakePrometheus(map[string]string{
		"sum(vllm:num_requests_waiting)": "2",
		"sum(rate(vllm:prefix_cache_hits_total[2m])) / sum(rate(vllm:prefix_cache_queries_total[2m]))": "NaN",
	})
	defer server.Close()
	sample := New(server.URL).Sample()
	if _, ok := sample.Value(core.MetricPrefixCacheHitRate); ok {
		t.Fatal("NaN was accepted as a prefix cache hit rate")
	}
}

// A per-second sampler must not re-probe every dead metric name on every tick.
func TestAbsentMetricsAreNotReprobedEverySample(t *testing.T) {
	fake, server := newFakePrometheus(map[string]string{"sum(vllm:num_requests_waiting)": "1"})
	defer server.Close()
	client := New(server.URL)

	client.Sample()
	fake.mu.Lock()
	first := fake.requests
	fake.mu.Unlock()

	client.Sample()
	fake.mu.Lock()
	second := fake.requests - first
	fake.mu.Unlock()

	if second >= first {
		t.Fatalf("second sample issued %d queries after the first issued %d; absent metrics are being re-probed", second, first)
	}
}
