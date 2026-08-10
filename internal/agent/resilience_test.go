package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

// A load generator that keeps hammering a dead target at full concurrency
// recreates the outage it is trying to measure past: the instant the target
// recovers, every pent-up VU hits it at once. The circuit breaker exists so a
// string of consecutive failures makes the generator back off instead.
func TestBackoffDelayGrowsThenCaps(t *testing.T) {
	if got := backoffDelay(circuitBreakerThreshold); got <= 0 || got > baseBackoff {
		t.Fatalf("first backoff should be small, got %v", got)
	}
	previous := time.Duration(0)
	for failures := int64(circuitBreakerThreshold); failures < circuitBreakerThreshold+10; failures++ {
		// Jitter makes any single draw noisy; compare the deterministic upper
		// bound (delay before the +/- jitter halves it) instead of the draw.
		shift := failures - circuitBreakerThreshold
		if shift > 6 {
			shift = 6
		}
		upperBound := baseBackoff << uint(shift)
		if upperBound > maxBackoff {
			upperBound = maxBackoff
		}
		if upperBound < previous {
			t.Fatalf("backoff bound must not shrink as failures grow: %v then %v", previous, upperBound)
		}
		previous = upperBound
	}
	if got := backoffDelay(circuitBreakerThreshold + 100); got > maxBackoff {
		t.Fatalf("backoff must cap at %v, got %v", maxBackoff, got)
	}
}

func TestNoBackoffBelowThreshold(t *testing.T) {
	m := &measurements{statusCounts: map[string]int64{}, timeline: map[int64]*timelineMeasurement{}, scenarios: map[string]*scenarioMeasurement{}}
	for i := 0; i < circuitBreakerThreshold-1; i++ {
		m.consecutiveFailures.Add(1)
	}
	start := time.Now()
	m.backoffBeforeAttempt(context.Background())
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("a run with fewer than %d consecutive failures should not back off, waited %v", circuitBreakerThreshold, elapsed)
	}
	if m.backoffEvents.Load() != 0 {
		t.Fatal("no backoff event should be recorded below the threshold")
	}
}

func TestBackoffEngagesAfterConsecutiveFailuresAndResetsOnSuccess(t *testing.T) {
	m := &measurements{statusCounts: map[string]int64{}, timeline: map[int64]*timelineMeasurement{}, scenarios: map[string]*scenarioMeasurement{}}
	for i := 0; i < circuitBreakerThreshold; i++ {
		m.recordTransportError(m.beginAttempt("", time.Now()), "connection refused")
	}
	start := time.Now()
	m.backoffBeforeAttempt(context.Background())
	if elapsed := time.Since(start); elapsed < baseBackoff/4 {
		t.Fatalf("after %d consecutive failures the generator should have waited, only waited %v", circuitBreakerThreshold, elapsed)
	}
	if m.backoffEvents.Load() != 1 {
		t.Fatalf("expected one backoff event, got %d", m.backoffEvents.Load())
	}

	// A single success must clear the breaker: the target recovering should not
	// leave every future attempt paying a stale penalty.
	m.recordSuccess(m.beginAttempt("", time.Now()), 5, modelTiming{}, 200, core.TokenUsage{}, core.RunConfig{})
	if m.consecutiveFailures.Load() != 0 {
		t.Fatal("a success must reset the consecutive failure count")
	}
	start = time.Now()
	m.backoffBeforeAttempt(context.Background())
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("the breaker should be closed again right after a success, waited %v", elapsed)
	}
}

// A dead target must not get hit at the generator's full local-loopback speed
// for the whole run: once it has failed a handful of times in a row, the
// circuit breaker should visibly throttle how often it gets re-attempted.
func TestDeadTargetIsNotHammeredForTheWholeRun(t *testing.T) {
	var attempts atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 2})

	if result.BackoffEvents == 0 {
		t.Fatalf("expected the circuit breaker to engage against a target failing on every request: %+v", result)
	}
	// Without backoff, one VU against a local httptest server would fire many
	// thousands of requests in 2 seconds. With it, most of the 2 seconds is
	// spent waiting out backoff between attempts.
	if attempts.Load() > 50 {
		t.Fatalf("backoff should have throttled retries against the failing target, saw %d attempts", attempts.Load())
	}
}

// The moment a target recovers, every VU that was backing off must not pile
// back on at once - each draw of backoffDelay carries its own jitter, so
// concurrent VUs at the same failure count don't retry in lockstep.
func TestJitterSpreadsOutSimultaneousRetries(t *testing.T) {
	const failures = circuitBreakerThreshold + 4
	var wg sync.WaitGroup
	delays := make([]time.Duration, 20)
	for i := range delays {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			delays[i] = backoffDelay(failures)
		}(i)
	}
	wg.Wait()

	distinct := map[time.Duration]bool{}
	for _, d := range delays {
		distinct[d] = true
		if d <= 0 || d > maxBackoff {
			t.Fatalf("backoff delay out of range: %v", d)
		}
	}
	if len(distinct) < len(delays)/2 {
		t.Fatalf("expected jitter to spread retries out, got %d distinct delays out of %d draws: %v", len(distinct), len(delays), delays)
	}
}

func TestLoadTransportPoolsConnectionsPerHost(t *testing.T) {
	transport := loadTransport()
	if transport.MaxIdleConnsPerHost <= http.DefaultMaxIdleConnsPerHost {
		t.Fatalf("load testing one host needs more than the %d-connection default, got %d", http.DefaultMaxIdleConnsPerHost, transport.MaxIdleConnsPerHost)
	}
}
