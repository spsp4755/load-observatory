package core

import "fmt"

// ServerConfig is the model server configuration a capacity number depends on.
// Two runs measured under different settings are not comparable, so this travels
// with every run: one half entered by the operator, the other scraped from the
// server, and any contradiction between them surfaced rather than reconciled.
//
// A zero value means unknown. Nothing here is invented or defaulted, because a
// guessed setting is worse than an admitted gap.
type ServerConfig struct {
	Version string `json:"version,omitempty"`
	Model   string `json:"model,omitempty"`
	// MaxNumSeqs is the concurrency ceiling. When execution pins to it while
	// requests queue, the limit is the configuration rather than the hardware.
	MaxNumSeqs int `json:"max_num_seqs,omitempty"`
	// MaxNumBatchedTokens is the per-step token budget. Chunked prefill is on by
	// default, which spreads a long prefill over several steps, so TTFT is a
	// function of this budget and not of prompt length alone. A TTFT reported
	// without it cannot be compared to another run's.
	MaxNumBatchedTokens  int     `json:"max_num_batched_tokens,omitempty"`
	GPUMemoryUtilization float64 `json:"gpu_memory_utilization,omitempty"`
	BlockSize            int     `json:"block_size,omitempty"`
	TensorParallelSize   int     `json:"tensor_parallel_size,omitempty"`
	// Tri-state as text: "on", "off", or empty for unknown.
	PrefixCaching  string `json:"prefix_caching,omitempty"`
	ChunkedPrefill string `json:"chunked_prefill,omitempty"`
}

// Known metric label names used when scraping the server's own config.
const (
	LabelVersion              = "version"
	LabelModel                = "model_name"
	LabelBlockSize            = "block_size"
	LabelGPUMemoryUtilization = "gpu_memory_utilization"
	LabelPrefixCaching        = "enable_prefix_caching"
	LabelChunkedPrefill       = "enable_chunked_prefill"
	LabelMaxNumSeqs           = "max_num_seqs"
	LabelMaxNumBatchedTokens  = "max_num_batched_tokens"
)

// provenanceField pairs a human label with a way to read it from a ServerConfig,
// so the gap report and the comparison report cannot drift apart.
type provenanceField struct {
	Label string
	Value func(ServerConfig) string
	// AffectsTTFT marks settings that change what a TTFT number means.
	AffectsTTFT bool
}

var provenanceFields = []provenanceField{
	{Label: "vLLM 버전", Value: func(c ServerConfig) string { return c.Version }},
	{Label: "모델", Value: func(c ServerConfig) string { return c.Model }},
	{Label: "max_num_seqs", Value: func(c ServerConfig) string { return intText(c.MaxNumSeqs) }},
	{Label: "max_num_batched_tokens", Value: func(c ServerConfig) string { return intText(c.MaxNumBatchedTokens) }, AffectsTTFT: true},
	{Label: "gpu_memory_utilization", Value: func(c ServerConfig) string { return floatText(c.GPUMemoryUtilization) }},
	{Label: "block_size", Value: func(c ServerConfig) string { return intText(c.BlockSize) }},
	{Label: "tensor_parallel_size", Value: func(c ServerConfig) string { return intText(c.TensorParallelSize) }},
	{Label: "prefix caching", Value: func(c ServerConfig) string { return c.PrefixCaching }, AffectsTTFT: true},
	{Label: "chunked prefill", Value: func(c ServerConfig) string { return c.ChunkedPrefill }, AffectsTTFT: true},
}

func intText(value int) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

func floatText(value float64) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%.2f", value)
}

// EffectiveServerConfig merges what the operator entered with what was scraped
// from the server. The scraped value wins, because the server is the authority on
// its own configuration.
func EffectiveServerConfig(entered, detected ServerConfig) ServerConfig {
	merged := entered
	if detected.Version != "" {
		merged.Version = detected.Version
	}
	if detected.Model != "" {
		merged.Model = detected.Model
	}
	if detected.MaxNumSeqs > 0 {
		merged.MaxNumSeqs = detected.MaxNumSeqs
	}
	if detected.MaxNumBatchedTokens > 0 {
		merged.MaxNumBatchedTokens = detected.MaxNumBatchedTokens
	}
	if detected.GPUMemoryUtilization > 0 {
		merged.GPUMemoryUtilization = detected.GPUMemoryUtilization
	}
	if detected.BlockSize > 0 {
		merged.BlockSize = detected.BlockSize
	}
	if detected.TensorParallelSize > 0 {
		merged.TensorParallelSize = detected.TensorParallelSize
	}
	if detected.PrefixCaching != "" {
		merged.PrefixCaching = detected.PrefixCaching
	}
	if detected.ChunkedPrefill != "" {
		merged.ChunkedPrefill = detected.ChunkedPrefill
	}
	return merged
}

// ProvenanceConflicts lists settings where the operator's entry disagrees with
// what the server reports. An operator reasoning from the wrong max_num_seqs will
// reach the wrong conclusion about whether a limit is configuration or hardware.
func ProvenanceConflicts(entered, detected ServerConfig) []string {
	var conflicts []string
	for _, field := range provenanceFields {
		mine, theirs := field.Value(entered), field.Value(detected)
		if mine == "" || theirs == "" || mine == theirs {
			continue
		}
		conflicts = append(conflicts, fmt.Sprintf("%s: 입력값 %s, 서버 보고값 %s", field.Label, mine, theirs))
	}
	return conflicts
}

// ProvenanceGaps lists the settings still unknown. Any gap in a TTFT-affecting
// setting means this run's TTFT cannot be compared with another's.
func ProvenanceGaps(config ServerConfig) (gaps []string, ttftComparable bool) {
	ttftComparable = true
	for _, field := range provenanceFields {
		if field.Value(config) != "" {
			continue
		}
		gaps = append(gaps, field.Label)
		if field.AffectsTTFT {
			ttftComparable = false
		}
	}
	return gaps, ttftComparable
}

// ProvenanceDifferences lists the settings that differ between two runs. Two
// capacity numbers measured under different server settings are not comparable,
// and this is what lets the UI say so instead of showing a misleading delta.
func ProvenanceDifferences(left, right ServerConfig) []string {
	var differences []string
	for _, field := range provenanceFields {
		a, b := field.Value(left), field.Value(right)
		if a == b {
			continue
		}
		differences = append(differences, fmt.Sprintf("%s: %s ↔ %s", field.Label, orUnknown(a), orUnknown(b)))
	}
	return differences
}

func orUnknown(value string) string {
	if value == "" {
		return "미확인"
	}
	return value
}

// ServerConfigFromLabels reads a ServerConfig out of Prometheus metric labels.
// Label names differ between engine versions, so an unrecognised or unparsable
// label is skipped rather than guessed at.
func ServerConfigFromLabels(labels map[string]string) ServerConfig {
	config := ServerConfig{
		Version:        labels[LabelVersion],
		Model:          labels[LabelModel],
		PrefixCaching:  boolLabel(labels[LabelPrefixCaching]),
		ChunkedPrefill: boolLabel(labels[LabelChunkedPrefill]),
	}
	config.BlockSize = intLabel(labels[LabelBlockSize])
	config.MaxNumSeqs = intLabel(labels[LabelMaxNumSeqs])
	config.MaxNumBatchedTokens = intLabel(labels[LabelMaxNumBatchedTokens])
	config.GPUMemoryUtilization = floatLabel(labels[LabelGPUMemoryUtilization])
	return config
}

// boolLabel normalises the several spellings engines use for a boolean label.
func boolLabel(raw string) string {
	switch raw {
	case "True", "true", "1", "on":
		return "on"
	case "False", "false", "0", "off":
		return "off"
	}
	return ""
}

func intLabel(raw string) int {
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil || value <= 0 {
		return 0
	}
	return value
}

func floatLabel(raw string) float64 {
	var value float64
	if _, err := fmt.Sscanf(raw, "%f", &value); err != nil || value <= 0 {
		return 0
	}
	return value
}
