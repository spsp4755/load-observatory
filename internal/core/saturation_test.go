package core

import (
	"strings"
	"testing"
)

func sample(second int64, metrics map[string]float64) MonitoringSample {
	return MonitoringSample{AtSecond: second, Status: "collected", Backend: "vllm", Metrics: metrics}
}

func samples(count int, metrics map[string]float64) []MonitoringSample {
	out := make([]MonitoringSample, 0, count)
	for i := range count {
		out = append(out, sample(int64(i), metrics))
	}
	return out
}

// The most common way an LLM capacity test lies: vLLM deliberately fills the KV
// cache to maximise batch size, so a high KV usage with an empty queue is
// healthy operation and must never be reported as saturation.
func TestFullKVCacheWithNoQueueIsHeadroomNotSaturation(t *testing.T) {
	verdict := AssessSaturation(samples(20, map[string]float64{
		MetricKVCacheUsage:    0.93,
		MetricRequestsWaiting: 0,
		MetricRequestsRunning: 12,
		MetricPreemptionRate:  0,
	}), RunConfig{})
	if verdict.State != SaturationHeadroom {
		t.Fatalf("a full KV cache with no queue was called %q: %s", verdict.State, verdict.Headline)
	}
	if !strings.Contains(verdict.Detail, "정상") {
		t.Fatalf("verdict does not explain that a full KV cache is normal: %q", verdict.Detail)
	}
}

// Saturation requires sustained queueing AND preemption together.
func TestSustainedQueueWithPreemptionIsSaturation(t *testing.T) {
	verdict := AssessSaturation(samples(20, map[string]float64{
		MetricKVCacheUsage:    0.98,
		MetricRequestsWaiting: 7,
		MetricRequestsRunning: 32,
		MetricPreemptionRate:  1.5,
	}), RunConfig{})
	if verdict.State != SaturationSaturated {
		t.Fatalf("sustained queueing with preemption was called %q", verdict.State)
	}
	if verdict.PreemptionRate == 0 || verdict.PeakWaiting == 0 {
		t.Fatalf("verdict did not carry the signals it rests on: %+v", verdict)
	}
}

// Queueing while execution is pinned at the configured ceiling is a config
// limit, not a hardware limit — the fix is a setting, not a GPU purchase.
func TestQueueingAtMaxNumSeqsIsConfigLimited(t *testing.T) {
	verdict := AssessSaturation(samples(20, map[string]float64{
		MetricRequestsWaiting: 5,
		MetricRequestsRunning: 16,
		MetricKVCacheUsage:    0.4,
		MetricPreemptionRate:  0,
	}), RunConfig{Server: ServerConfig{MaxNumSeqs: 16}})
	if verdict.State != SaturationConfigLimited {
		t.Fatalf("queueing at max_num_seqs was called %q", verdict.State)
	}
	if !strings.Contains(verdict.Detail, "GPU 증설이 아니라") {
		t.Fatalf("verdict does not steer away from buying hardware: %q", verdict.Detail)
	}
}

// A single spike is not a sustained queue.
func TestSingleQueueSpikeIsNotSaturation(t *testing.T) {
	series := samples(20, map[string]float64{MetricRequestsWaiting: 0, MetricKVCacheUsage: 0.5, MetricPreemptionRate: 0})
	series[7].Metrics = map[string]float64{MetricRequestsWaiting: 4, MetricKVCacheUsage: 0.5, MetricPreemptionRate: 0}
	if verdict := AssessSaturation(series, RunConfig{}); verdict.State != SaturationHeadroom {
		t.Fatalf("one spike in 20 seconds was called %q: %s", verdict.State, verdict.Headline)
	}
}

func TestSaturationIsUnknownWithoutServerMetrics(t *testing.T) {
	if verdict := AssessSaturation(nil, RunConfig{}); verdict.State != SaturationUnknown {
		t.Fatalf("expected unknown without samples, got %q", verdict.State)
	}
	// A sample that collected nothing must not read as "no queue, all healthy".
	empty := []MonitoringSample{{Status: "unavailable"}}
	if verdict := AssessSaturation(empty, RunConfig{}); verdict.State != SaturationUnknown {
		t.Fatalf("an empty sample was treated as data: %q", verdict.State)
	}
}

// An absent DCGM profiling field must not read as a zero measurement.
func TestAbsentMetricsAreNotTreatedAsZero(t *testing.T) {
	verdict := AssessSaturation(samples(10, map[string]float64{MetricKVCacheUsage: 0.6}), RunConfig{})
	if verdict.State != SaturationHeadroom {
		t.Fatalf("expected headroom from KV alone, got %q", verdict.State)
	}
	if verdict.PeakRunning != 0 || verdict.AvgRunning != 0 {
		t.Fatal("running was never collected, so it must stay zero-valued rather than invented")
	}
}

func TestThrottlingInvalidatesTheRun(t *testing.T) {
	validity := AssessRunValidity(samples(10, map[string]float64{MetricThermalViolationRate: 0.4}), RunResult{}, RunConfig{})
	if validity.Trustworthy {
		t.Fatal("a thermally throttled run was reported as trustworthy")
	}
	if len(validity.Reasons) == 0 || !strings.Contains(validity.Reasons[0], "온도") {
		t.Fatalf("reason does not name thermal throttling: %v", validity.Reasons)
	}
}

func TestXIDErrorInvalidatesTheRun(t *testing.T) {
	if validity := AssessRunValidity(samples(5, map[string]float64{MetricXIDErrors: 79}), RunResult{}, RunConfig{}); validity.Trustworthy {
		t.Fatal("a run with a GPU XID error was reported as trustworthy")
	}
}

// A falling clock is the ground truth for throttling even when the violation
// counters are not exported.
func TestFallingSMClockInvalidatesTheRun(t *testing.T) {
	series := []MonitoringSample{
		sample(0, map[string]float64{MetricSMClockMHz: 1900}),
		sample(1, map[string]float64{MetricSMClockMHz: 1890}),
		sample(2, map[string]float64{MetricSMClockMHz: 1600}),
		sample(3, map[string]float64{MetricSMClockMHz: 1500}),
	}
	if validity := AssessRunValidity(series, RunResult{}, RunConfig{}); validity.Trustworthy {
		t.Fatal("a run whose SM clock fell 21% was reported as trustworthy")
	}
	steady := samples(6, map[string]float64{MetricSMClockMHz: 1900})
	if validity := AssessRunValidity(steady, RunResult{}, RunConfig{}); !validity.Trustworthy {
		t.Fatalf("a steady clock was flagged: %v", validity.Reasons)
	}
}

// If the cache policy says bypass but the server reports prefix cache hits, the
// prompts were not actually bypassing the cache and TTFT is optimistic.
func TestPrefixCacheHitRateContradictingTheCachePolicyInvalidatesTheRun(t *testing.T) {
	hot := samples(10, map[string]float64{MetricPrefixCacheHitRate: 0.95})
	if validity := AssessRunValidity(hot, RunResult{}, RunConfig{CachePolicy: CachePolicyBypass}); validity.Trustworthy {
		t.Fatal("bypass policy with a 95% prefix cache hit rate was reported as trustworthy")
	}
	if validity := AssessRunValidity(hot, RunResult{}, RunConfig{CachePolicy: CachePolicyReuse}); !validity.Trustworthy {
		t.Fatalf("reuse policy with a high hit rate is expected, not a problem: %v", validity.Reasons)
	}
	cold := samples(10, map[string]float64{MetricPrefixCacheHitRate: 0.02})
	if validity := AssessRunValidity(cold, RunResult{}, RunConfig{CachePolicy: CachePolicyReuse}); validity.Trustworthy {
		t.Fatal("reuse policy with a 2% hit rate should warn that prefix caching may be off")
	}
	if validity := AssessRunValidity(cold, RunResult{}, RunConfig{CachePolicy: CachePolicyBypass}); !validity.Trustworthy {
		t.Fatalf("bypass policy with a cold cache is the goal, not a problem: %v", validity.Reasons)
	}
}

func TestCleanRunIsTrustworthy(t *testing.T) {
	clean := samples(10, map[string]float64{
		MetricSMClockMHz: 1900, MetricXIDErrors: 0, MetricThermalViolationRate: 0,
		MetricPowerViolationRate: 0, MetricPrefixCacheHitRate: 0.05,
	})
	if validity := AssessRunValidity(clean, RunResult{}, RunConfig{CachePolicy: CachePolicyBypass}); !validity.Trustworthy {
		t.Fatalf("a clean run was flagged: %v", validity.Reasons)
	}
}

// The check that stops the tool blaming the model for the load generator's own
// latency.
func TestClientTTFTExplainedByServerPhasesIsServerBound(t *testing.T) {
	attribution := AttributeTTFT(
		RunResult{TTFT: Distribution{P95Millis: 1000}},
		samples(10, map[string]float64{MetricQueueTimeP95: 300, MetricPrefillTimeP95: 650}),
	)
	if !attribution.Available || attribution.Verdict != AttributionServer {
		t.Fatalf("expected server_bound, got %q: %s", attribution.Verdict, attribution.Headline)
	}
	if attribution.UnaccountedPercent > 10 {
		t.Fatalf("unaccounted share should be small: %.1f%%", attribution.UnaccountedPercent)
	}
}

func TestClientTTFTUnexplainedByServerPhasesBlamesTheClientPath(t *testing.T) {
	attribution := AttributeTTFT(
		RunResult{TTFT: Distribution{P95Millis: 4000}},
		samples(10, map[string]float64{MetricQueueTimeP95: 100, MetricPrefillTimeP95: 200}),
	)
	if attribution.Verdict != AttributionExternal {
		t.Fatalf("expected client_or_network_bound, got %q", attribution.Verdict)
	}
	if !strings.Contains(attribution.Headline, "부하 발생기") {
		t.Fatalf("headline does not point at the client path: %q", attribution.Headline)
	}
	if attribution.UnaccountedMillis != 3700 {
		t.Fatalf("unaccounted millis wrong: %.0f", attribution.UnaccountedMillis)
	}
}

func TestAttributionIsUnknownWithoutServerPhaseMetrics(t *testing.T) {
	attribution := AttributeTTFT(RunResult{TTFT: Distribution{P95Millis: 1000}}, samples(5, map[string]float64{MetricKVCacheUsage: 0.5}))
	if attribution.Available || attribution.Verdict != AttributionUnknown {
		t.Fatalf("expected unknown without queue/prefill metrics, got %q", attribution.Verdict)
	}
}

// Accounted time larger than the observed TTFT must never become negative
// unaccounted time, and it is a metrics mismatch rather than a server verdict.
func TestAttributionNeverReportsNegativeUnaccountedTime(t *testing.T) {
	attribution := AttributeTTFT(
		RunResult{TTFT: Distribution{P95Millis: 500}},
		samples(5, map[string]float64{MetricQueueTimeP95: 400, MetricPrefillTimeP95: 400}),
	)
	if attribution.UnaccountedMillis < 0 || attribution.UnaccountedPercent < 0 {
		t.Fatalf("negative unaccounted time: %+v", attribution)
	}
	if attribution.Verdict != AttributionMismatch {
		t.Fatalf("800ms of server time under a 500ms client TTFT is a mismatch, got %q", attribution.Verdict)
	}
}

// Exactly-accounted TTFT is the server's.
func TestFullyAccountedTTFTIsServerBound(t *testing.T) {
	attribution := AttributeTTFT(
		RunResult{TTFT: Distribution{P95Millis: 800}},
		samples(5, map[string]float64{MetricQueueTimeP95: 400, MetricPrefillTimeP95: 400}),
	)
	if attribution.Verdict != AttributionServer {
		t.Fatalf("fully accounted TTFT should be server_bound, got %q", attribution.Verdict)
	}
	if attribution.UnaccountedMillis != 0 {
		t.Fatalf("expected nothing unaccounted, got %.0f", attribution.UnaccountedMillis)
	}
}

// The server cannot have spent longer on our request than our client waited. When
// the scraped metrics say it did, they describe other traffic on the same server,
// and reporting "server_bound" from them would be a fabricated decomposition.
func TestServerPhasesExceedingClientTTFTIsReportedAsAMetricsMismatch(t *testing.T) {
	attribution := AttributeTTFT(
		RunResult{TTFT: Distribution{P95Millis: 181}},
		samples(10, map[string]float64{MetricQueueTimeP95: 2400, MetricPrefillTimeP95: 300}),
	)
	if attribution.Verdict != AttributionMismatch {
		t.Fatalf("expected metrics_not_this_run, got %q: %s", attribution.Verdict, attribution.Headline)
	}
	if !strings.Contains(attribution.Headline, "다른 트래픽") {
		t.Fatalf("headline does not explain that the metrics cover other traffic: %q", attribution.Headline)
	}
}

// Small overshoot is sampling noise, not a mismatch.
func TestSmallServerOvershootStaysServerBound(t *testing.T) {
	attribution := AttributeTTFT(
		RunResult{TTFT: Distribution{P95Millis: 1000}},
		samples(10, map[string]float64{MetricQueueTimeP95: 600, MetricPrefillTimeP95: 480}),
	)
	if attribution.Verdict != AttributionServer {
		t.Fatalf("an 8%% overshoot should stay server_bound, got %q", attribution.Verdict)
	}
}
