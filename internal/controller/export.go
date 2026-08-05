package controller

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/spsp4755/load-observatory/internal/core"
)

// exportRun writes a run as JSON or CSV for a report. The JSON form is the same
// runView the UI reads, verdicts included, so an attached file carries the
// reasoning and the provenance rather than bare numbers.
func (s *Server) exportRun(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	id := strings.TrimSuffix(strings.TrimSuffix(path, "/export.csv"), "/export.json")
	run, ok := s.store.GetRun(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(path, "/export.json") {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".json"))
		writeJSON(w, http.StatusOK, view(run))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".csv"))
	writeRunCSV(w, view(run))
}

// writeRunCSV emits a long-format table: one row per metric. A run has scalars,
// distributions, per-second points and per-scenario rows, and forcing those into
// one wide header would either lose data or invent empty columns.
func writeRunCSV(w http.ResponseWriter, item runView) {
	out := csv.NewWriter(w)
	defer out.Flush()
	_ = out.Write([]string{"section", "key", "subkey", "value"})

	write := func(section, key, subkey string, value any) {
		_ = out.Write([]string{section, key, subkey, fmt.Sprint(value)})
	}
	result := item.Result

	write("run", "id", "", item.ID)
	write("run", "status", "", item.Status)
	write("run", "load", "", loadOf(item.Config))
	write("run", "duration_seconds", "", item.Config.DurationSeconds)
	write("run", "drain_seconds", "", item.Config.DrainSeconds)
	write("run", "steady_state_seconds", "", item.Config.SteadyStateSeconds)
	write("run", "cache_policy", "", item.Config.CachePolicy)
	write("run", "output_length_pinned", "", result.OutputLengthPinned)
	write("run", "context_accumulated", "", result.ContextAccumulated)

	// Provenance first: without it the numbers below are not comparable.
	server := item.Provenance.Server
	write("provenance", "version", "", server.Version)
	write("provenance", "model", "", server.Model)
	write("provenance", "max_num_seqs", "", server.MaxNumSeqs)
	write("provenance", "max_num_batched_tokens", "", server.MaxNumBatchedTokens)
	write("provenance", "gpu_memory_utilization", "", server.GPUMemoryUtilization)
	write("provenance", "block_size", "", server.BlockSize)
	write("provenance", "tensor_parallel_size", "", server.TensorParallelSize)
	write("provenance", "prefix_caching", "", server.PrefixCaching)
	write("provenance", "chunked_prefill", "", server.ChunkedPrefill)
	write("provenance", "ttft_comparable", "", item.Provenance.TTFTComparable)
	for _, gap := range item.Provenance.Gaps {
		write("provenance", "unknown", "", gap)
	}
	for _, conflict := range item.Provenance.Conflicts {
		write("provenance", "conflict", "", conflict)
	}

	write("lifecycle", "issued", "", result.Issued)
	write("lifecycle", "completed", "", result.Completed)
	write("lifecycle", "cancelled", "", result.Cancelled)
	write("lifecycle", "http_failures", "", result.HTTPFailures)
	write("lifecycle", "transport_errors", "", result.TransportErrors)
	write("lifecycle", "completion_percent", "", result.CompletionPercent)
	write("lifecycle", "dropped_arrivals", "", result.DroppedArrivals)

	write("throughput", "requests_per_second", "", result.ThroughputRPS)
	write("throughput", "output_tokens_per_second", "", result.Tokens.OutputPerSecond)
	write("throughput", "prompt_tokens", "", result.Tokens.Prompt)
	write("throughput", "completion_tokens", "", result.Tokens.Completion)
	write("throughput", "goodput_percent", "", result.GoodputPercent)

	write("steady_state", "seconds", "", result.SteadySeconds)
	write("steady_state", "samples", "", result.SteadySamples)
	for name, distribution := range map[string]core.Distribution{
		"latency": result.Latency, "ttft": result.TTFT, "ttfo": result.TTFO,
		"itl": result.ITL, "tpot": result.TPOT,
	} {
		write("distribution", name, "min_millis", distribution.MinMillis)
		write("distribution", name, "avg_millis", distribution.AvgMillis)
		write("distribution", name, "p50_millis", distribution.P50Millis)
		write("distribution", name, "p95_millis", distribution.P95Millis)
		write("distribution", name, "p99_millis", distribution.P99Millis)
		write("distribution", name, "max_millis", distribution.MaxMillis)
	}

	write("verdict", "saturation", "", item.Saturation.State)
	write("verdict", "saturation_headline", "", item.Saturation.Headline)
	write("verdict", "trustworthy", "", item.Validity.Trustworthy)
	for _, reason := range item.Validity.Reasons {
		write("verdict", "untrustworthy_reason", "", reason)
	}
	write("verdict", "attribution", "", item.Attribution.Verdict)
	write("verdict", "attribution_headline", "", item.Attribution.Headline)

	for _, scenario := range result.Scenarios {
		write("scenario", scenario.Name, "issued", scenario.Issued)
		write("scenario", scenario.Name, "completed", scenario.Completed)
		write("scenario", scenario.Name, "completion_percent", scenario.CompletionPercent)
		write("scenario", scenario.Name, "cancelled", scenario.Cancelled)
		write("scenario", scenario.Name, "failures", scenario.Failures)
		write("scenario", scenario.Name, "latency_p50_millis", scenario.Latency.P50Millis)
		write("scenario", scenario.Name, "latency_p95_millis", scenario.Latency.P95Millis)
		write("scenario", scenario.Name, "ttft_p95_millis", scenario.TTFT.P95Millis)
		write("scenario", scenario.Name, "input_tokens", scenario.InputTokens)
		write("scenario", scenario.Name, "output_tokens", scenario.OutputTokens)
	}

	for _, point := range result.Timeline {
		second := strconv.FormatInt(point.Second, 10)
		write("timeline", second, "target_load", point.TargetLoad)
		write("timeline", second, "active", point.Active)
		write("timeline", second, "issued", point.Issued)
		write("timeline", second, "completed", point.Completed)
		write("timeline", second, "cancelled", point.Cancelled)
		write("timeline", second, "p95_millis", point.P95Millis)
	}

	for _, sample := range item.Monitoring {
		second := strconv.FormatInt(sample.AtSecond, 10)
		for key, value := range sample.Metrics {
			write("server_metrics", second, key, value)
		}
	}
}

func loadOf(config core.RunConfig) string {
	if config.Mode == core.LoadModeRPS {
		return fmt.Sprintf("%d RPS", config.RPS)
	}
	return fmt.Sprintf("%d VU", config.VUs)
}
