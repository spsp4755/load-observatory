package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

// Uniform arrivals are evenly spaced; Poisson arrivals are not. Real callers do
// not arrive on a metronome, and queueing only appears under bursty arrivals.
func TestPoissonArrivalsVaryTheGapWhileUniformDoesNot(t *testing.T) {
	uniform := newArrivalSchedule(core.ArrivalUniform, 10)
	for range 5 {
		if gap := uniform.next(); gap != 100*time.Millisecond {
			t.Fatalf("uniform gap %v, want 100ms", gap)
		}
	}

	poisson := newArrivalSchedule(core.ArrivalPoisson, 10)
	var gaps []time.Duration
	var total time.Duration
	for range 400 {
		gap := poisson.next()
		gaps = append(gaps, gap)
		total += gap
	}
	distinct := map[time.Duration]bool{}
	for _, gap := range gaps {
		distinct[gap] = true
		if gap < 0 {
			t.Fatalf("negative gap %v", gap)
		}
	}
	if len(distinct) < 100 {
		t.Fatalf("only %d distinct gaps in 400 draws; not a real distribution", len(distinct))
	}
	// The mean must still hold the requested rate, within sampling slack.
	mean := total / time.Duration(len(gaps))
	if mean < 70*time.Millisecond || mean > 130*time.Millisecond {
		t.Fatalf("mean gap %v drifts from the 100ms target rate", mean)
	}
}

// The same configuration must issue the same arrival pattern, or two runs are not
// comparable.
func TestArrivalScheduleIsReproducible(t *testing.T) {
	first := newArrivalSchedule(core.ArrivalPoisson, 20)
	second := newArrivalSchedule(core.ArrivalPoisson, 20)
	for i := range 50 {
		if a, b := first.next(), second.next(); a != b {
			t.Fatalf("draw %d differs between identical schedules: %v vs %v", i, a, b)
		}
	}
}

// Latency measured from the send time hides queueing inside the generator. That
// is coordinated omission: one stall records a single slow sample instead of
// every request that should have gone out during it.
func TestRPSLatencyIsMeasuredFromTheIntendedArrival(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// 20 arrivals a second against a 300ms target with one slot: arrivals pile up
	// far faster than they can be served.
	result := Run(context.Background(), target.URL, core.RunConfig{
		Mode: core.LoadModeRPS, RPS: 20, DurationSeconds: 2, MaxInFlight: 1, DrainSeconds: 2,
	})
	if !result.LatencyFromIntendedArrival {
		t.Fatal("an RPS run must report that latency came from the intended arrival")
	}
	if result.Successes == 0 {
		t.Fatalf("no request succeeded: %+v", result)
	}
	// Every served request waited at least the 300ms service time; the ones that
	// queued waited longer, so the tail must exceed the service time.
	if result.Latency.MaxMillis < 300 {
		t.Fatalf("service time missing from the tail: max=%dms", result.Latency.MaxMillis)
	}
	// Arrivals beyond the in-flight limit are dropped rather than queued, which is
	// the honest choice only because the drop is counted. Without this number a
	// reported throughput would look like the target rate was actually served.
	if result.DroppedArrivals == 0 {
		t.Fatal("arrivals beyond the in-flight limit were neither served nor counted as dropped")
	}
}

// The accounting that prevents coordinated omission: a request sent late must be
// measured from when it was due, and the lateness reported separately so the
// generator can be identified as the bottleneck.
func TestLateSendIsChargedToTheGeneratorAndTheIntendedArrival(t *testing.T) {
	m := &measurements{
		started: time.Now().Add(-10 * time.Second), statusCounts: map[string]int64{},
		timeline: map[int64]*timelineMeasurement{}, scenarios: map[string]*scenarioMeasurement{},
	}
	// Due 2 seconds ago, sent now.
	due := time.Now().Add(-2 * time.Second)
	call := m.beginAttempt("", due)
	if !call.scheduledAt.Equal(due) {
		t.Fatalf("the intended arrival was not preserved: %v vs %v", call.scheduledAt, due)
	}
	if call.startedAt.Before(due) {
		t.Fatal("send time should be after the due time")
	}
	result := m.result(core.RunConfig{Mode: core.LoadModeRPS})
	if result.GeneratorDelay.MaxMillis < 1900 {
		t.Fatalf("a 2s late send was not charged to the generator: %+v", result.GeneratorDelay)
	}
}

// A send that is not late must not invent a delay, and a scheduled time in the
// future must not produce a negative one.
func TestOnTimeSendHasNoGeneratorDelay(t *testing.T) {
	m := &measurements{
		started: time.Now(), statusCounts: map[string]int64{},
		timeline: map[int64]*timelineMeasurement{}, scenarios: map[string]*scenarioMeasurement{},
	}
	m.beginAttempt("", time.Now())
	m.beginAttempt("", time.Now().Add(time.Hour))
	m.beginAttempt("", time.Time{})
	result := m.result(core.RunConfig{Mode: core.LoadModeRPS})
	if result.GeneratorDelay.MinMillis < 0 || result.GeneratorDelay.MaxMillis > 50 {
		t.Fatalf("an on-time send produced a delay: %+v", result.GeneratorDelay)
	}
}

// A closed-loop run has no intended arrival time, so it must not claim one.
func TestVURunDoesNotClaimIntendedArrivalLatency(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 1})
	if result.LatencyFromIntendedArrival {
		t.Fatal("a closed-loop run reported intended-arrival latency")
	}
	if result.GeneratorDelay.MaxMillis > 50 {
		t.Fatalf("a VU worker issues immediately, so its generator delay should be near zero: %dms", result.GeneratorDelay.MaxMillis)
	}
}

// Real traffic does not use a single output length or a single prompt size.
func TestOutputLengthAndPromptSizeVaryWhenAStdevIsSet(t *testing.T) {
	var mu sync.Mutex
	var maxTokens []int
	var promptLengths []int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MaxTokens int           `json:"max_tokens"`
			Messages  []chatMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		maxTokens = append(maxTokens, request.MaxTokens)
		if len(request.Messages) > 0 {
			promptLengths = append(promptLengths, len(request.Messages[0].Content))
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer target.Close()

	RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "m"}, core.RunConfig{
		Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 2, MaxTokens: 500, Prompt: "base",
		OutputTokensStdev: 200, PromptPadTokens: 100, PromptPadStdev: 50,
	})

	mu.Lock()
	defer mu.Unlock()
	if len(maxTokens) < 4 {
		t.Fatalf("too few requests to judge variation: %d", len(maxTokens))
	}
	if len(distinctInts(maxTokens)) < 3 {
		t.Fatalf("output length did not vary: %v", maxTokens)
	}
	for _, value := range maxTokens {
		if value < 1 {
			t.Fatalf("jitter produced a non-positive max_tokens: %v", maxTokens)
		}
	}
	if len(distinctInts(promptLengths)) < 3 {
		t.Fatalf("prompt size did not vary: %v", promptLengths)
	}
	// Padding must actually grow the prompt well past the base text.
	for _, length := range promptLengths {
		if length < 100 {
			t.Fatalf("prompt was not padded: %v", promptLengths)
		}
	}
}

// Without a stdev the workload must stay exactly as configured.
func TestWorkloadIsUnchangedWithoutAStdev(t *testing.T) {
	if got := jitter("osl", 7, 0); got != 0 {
		t.Fatalf("jitter with no stdev returned %d", got)
	}
	var mu sync.Mutex
	var seen []int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		seen = append(seen, request.MaxTokens)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer target.Close()

	RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "m"},
		core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 128, Prompt: "base"})
	mu.Lock()
	defer mu.Unlock()
	for _, value := range seen {
		if value != 128 {
			t.Fatalf("max_tokens changed without a stdev configured: %v", seen)
		}
	}
}

func distinctInts(values []int) map[int]bool {
	out := map[int]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
