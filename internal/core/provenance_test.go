package core

import (
	"strings"
	"testing"
)

// Chunked prefill is on by default and spreads a long prefill over several steps,
// so TTFT is a function of the token budget rather than of prompt length alone.
// Without that budget recorded, one run's TTFT cannot be compared with another's.
func TestTTFTIsNotComparableWithoutTheTokenBudget(t *testing.T) {
	gaps, comparable := ProvenanceGaps(ServerConfig{Version: "0.11.0", Model: "m", MaxNumSeqs: 256, BlockSize: 16, GPUMemoryUtilization: 0.9, TensorParallelSize: 1, PrefixCaching: "on", ChunkedPrefill: "on"})
	if comparable {
		t.Fatal("TTFT reported comparable with max_num_batched_tokens unknown")
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "max_num_batched_tokens") {
		t.Fatalf("gaps %v should name exactly the missing budget", gaps)
	}
}

func TestFullProvenanceMakesTTFTComparable(t *testing.T) {
	complete := ServerConfig{
		Version: "0.11.0", Model: "qwen", MaxNumSeqs: 256, MaxNumBatchedTokens: 8192,
		GPUMemoryUtilization: 0.9, BlockSize: 16, TensorParallelSize: 2,
		PrefixCaching: "on", ChunkedPrefill: "on",
	}
	gaps, comparable := ProvenanceGaps(complete)
	if !comparable || len(gaps) != 0 {
		t.Fatalf("complete provenance still reported gaps %v (comparable=%t)", gaps, comparable)
	}
}

// The server is the authority on its own configuration.
func TestDetectedConfigOverridesWhatTheOperatorTyped(t *testing.T) {
	entered := ServerConfig{MaxNumSeqs: 16, MaxNumBatchedTokens: 2048, Model: "guess"}
	detected := ServerConfig{MaxNumSeqs: 256, PrefixCaching: "on"}
	merged := EffectiveServerConfig(entered, detected)
	if merged.MaxNumSeqs != 256 {
		t.Fatalf("detected max_num_seqs did not win: %d", merged.MaxNumSeqs)
	}
	if merged.MaxNumBatchedTokens != 2048 {
		t.Fatal("an entered value the server did not report should survive")
	}
	if merged.PrefixCaching != "on" || merged.Model != "guess" {
		t.Fatalf("merge lost a field: %+v", merged)
	}
}

// An operator reasoning from the wrong max_num_seqs reaches the wrong conclusion
// about whether a limit is configuration or hardware, so the disagreement has to
// be surfaced rather than silently resolved.
func TestConflictBetweenEnteredAndDetectedConfigIsReported(t *testing.T) {
	conflicts := ProvenanceConflicts(ServerConfig{MaxNumSeqs: 16}, ServerConfig{MaxNumSeqs: 256})
	if len(conflicts) != 1 || !strings.Contains(conflicts[0], "16") || !strings.Contains(conflicts[0], "256") {
		t.Fatalf("conflict not reported with both values: %v", conflicts)
	}
	// Agreement, and an unknown on either side, are not conflicts.
	if got := ProvenanceConflicts(ServerConfig{MaxNumSeqs: 256}, ServerConfig{MaxNumSeqs: 256}); len(got) != 0 {
		t.Fatalf("agreement reported as conflict: %v", got)
	}
	if got := ProvenanceConflicts(ServerConfig{MaxNumSeqs: 16}, ServerConfig{}); len(got) != 0 {
		t.Fatalf("an unreported server value is a gap, not a conflict: %v", got)
	}
}

// Two capacity numbers measured under different settings are not comparable.
func TestProvenanceDifferencesListWhatChangedBetweenRuns(t *testing.T) {
	left := ServerConfig{Model: "qwen", MaxNumSeqs: 256, MaxNumBatchedTokens: 8192, ChunkedPrefill: "on"}
	right := ServerConfig{Model: "qwen", MaxNumSeqs: 256, MaxNumBatchedTokens: 2048, ChunkedPrefill: "on"}
	differences := ProvenanceDifferences(left, right)
	if len(differences) == 0 {
		t.Fatal("a changed token budget was not reported as a difference")
	}
	joined := strings.Join(differences, " | ")
	if !strings.Contains(joined, "8192") || !strings.Contains(joined, "2048") {
		t.Fatalf("difference does not show both values: %q", joined)
	}
	if strings.Contains(joined, "모델") {
		t.Fatalf("an identical field was reported as different: %q", joined)
	}
	// A field unknown on one side is still a difference worth flagging.
	if got := ProvenanceDifferences(left, ServerConfig{Model: "qwen", MaxNumSeqs: 256, MaxNumBatchedTokens: 8192}); len(got) == 0 {
		t.Fatal("an unknown setting on one side is not comparable and must be flagged")
	}
}

// vLLM publishes its cache config in metric labels, with engine-specific
// spellings for booleans.
func TestServerConfigIsReadFromMetricLabels(t *testing.T) {
	config := ServerConfigFromLabels(map[string]string{
		"model_name": "qwen/qwen3", "block_size": "16", "gpu_memory_utilization": "0.9",
		"enable_prefix_caching": "True", "enable_chunked_prefill": "False", "version": "0.11.0",
	})
	if config.Model != "qwen/qwen3" || config.BlockSize != 16 || config.Version != "0.11.0" {
		t.Fatalf("labels not read: %+v", config)
	}
	if config.GPUMemoryUtilization != 0.9 {
		t.Fatalf("float label not parsed: %v", config.GPUMemoryUtilization)
	}
	if config.PrefixCaching != "on" || config.ChunkedPrefill != "off" {
		t.Fatalf("Python-style booleans not normalised: %+v", config)
	}
	// An unparsable or absent label stays unknown rather than becoming zero.
	partial := ServerConfigFromLabels(map[string]string{"block_size": "not-a-number"})
	if partial.BlockSize != 0 || partial.PrefixCaching != "" {
		t.Fatalf("a bad label was guessed at: %+v", partial)
	}
}
