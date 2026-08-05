package core

const (
	MaxVUs             = 500
	MaxRPS             = 2000
	MaxDurationSeconds = 60 * 60
	MaxModelTokens     = 1000000
)

type LoadMode string

const (
	LoadModeVU  LoadMode = "vu"
	LoadModeRPS LoadMode = "rps"
)

type CachePolicy string

const (
	CachePolicyMixed  CachePolicy = "mixed"
	CachePolicyReuse  CachePolicy = "reuse"
	CachePolicyBypass CachePolicy = "bypass"
)

type TargetType string

const (
	TargetTypeWeb   TargetType = "web"
	TargetTypeModel TargetType = "model"
)

type Target struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Type      TargetType `json:"type"`
	URL       string     `json:"url"`
	Model     string     `json:"model,omitempty"`
	APIKey    string     `json:"api_key,omitempty"`
	HasAPIKey bool       `json:"has_api_key,omitempty"`
}

type RunConfig struct {
	TargetID                 string         `json:"target_id"`
	Mode                     LoadMode       `json:"mode"`
	VUs                      int            `json:"vus"`
	RPS                      int            `json:"rps"`
	DurationSeconds          int            `json:"duration_seconds"`
	Prompt                   string         `json:"prompt"`
	MaxTokens                int            `json:"max_tokens"`
	MaxErrorPercent          float64        `json:"max_error_percent"`
	MaxP95Millis             int64          `json:"max_p95_millis"`
	MaxTTFTP95Millis         int64          `json:"max_ttft_p95_millis,omitempty"`
	MinOutputTokensPerSecond float64        `json:"min_output_tokens_per_second,omitempty"`
	CachePolicy              CachePolicy    `json:"cache_policy"`
	VariationPercent         int            `json:"variation_percent"`
	WorkloadID               string         `json:"workload_id,omitempty"`
	Shards                   int            `json:"shards,omitempty"`
	MaxInFlight              int            `json:"max_in_flight,omitempty"`
	WarmupRequests           int            `json:"warmup_requests,omitempty"`
	CooldownSeconds          int            `json:"cooldown_seconds,omitempty"`
	MaxTTPOTP95Millis        int64          `json:"max_tpot_p95_millis,omitempty"`
	MinGoodputPercent        float64        `json:"min_goodput_percent,omitempty"`
	Stages                   []LoadStage    `json:"stages,omitempty"`
	Scenario                 []ScenarioTask `json:"scenario,omitempty"`
	AgentWorkflow            bool           `json:"agent_workflow,omitempty"`
	Journeys                 []UserJourney  `json:"journeys,omitempty"`
	DrainSeconds             int            `json:"drain_seconds,omitempty"`
	SteadyStateSeconds       int            `json:"steady_state_seconds,omitempty"`
	MinCompletionPercent     float64        `json:"min_completion_percent,omitempty"`
	// IgnoreEOS pins every response to exactly MaxTokens so TPOT and ITL are
	// comparable between runs. It is a vLLM/SGLang extension, so it is opt-in:
	// a server that rejects unknown fields would fail every request.
	IgnoreEOS bool `json:"ignore_eos,omitempty"`
	// Server is the model server's configuration as the operator knows it.
	// Capacity numbers are only comparable between runs that shared it.
	Server ServerConfig `json:"server,omitempty"`
	// AccumulateContext carries each turn's answer into the next request, the way
	// a real chat or agent session does. The prompt then grows every turn, which
	// is the dominant driver of real KV cache pressure and of TTFT growth. Off by
	// default because turning it on changes what is being measured.
	AccumulateContext bool `json:"accumulate_context,omitempty"`
}

type LoadStage struct {
	DurationSeconds int `json:"duration_seconds"`
	TargetLoad      int `json:"target_load"`
}

type ScenarioTask struct {
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	Weight          int    `json:"weight"`
	ThinkTimeMillis int    `json:"think_time_millis,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
}

type UserJourney struct {
	Name          string         `json:"name"`
	Weight        int            `json:"weight"`
	AgentWorkflow bool           `json:"agent_workflow,omitempty"`
	Scenario      []ScenarioTask `json:"scenario"`
}

type Distribution struct {
	MinMillis int64 `json:"min_millis"`
	AvgMillis int64 `json:"avg_millis"`
	P50Millis int64 `json:"p50_millis"`
	P95Millis int64 `json:"p95_millis"`
	P99Millis int64 `json:"p99_millis"`
	MaxMillis int64 `json:"max_millis"`
}

type TokenUsage struct {
	Prompt          int64   `json:"prompt"`
	Completion      int64   `json:"completion"`
	Reasoning       int64   `json:"reasoning"`
	OutputPerSecond float64 `json:"output_per_second"`
}

type TimelinePoint struct {
	Second    int64 `json:"second"`
	Requests  int64 `json:"requests"`
	Successes int64 `json:"successes"`
	Failures  int64 `json:"failures"`
	P95Millis int64 `json:"p95_millis"`
	// Issued counts requests started in this second; Completed counts those that
	// received a full HTTP response. Cancelled counts in-flight requests killed by
	// the run deadline. Active/Waiting/TargetLoad are gauges sampled once a second.
	Issued     int64 `json:"issued"`
	Completed  int64 `json:"completed"`
	Cancelled  int64 `json:"cancelled"`
	Active     int64 `json:"active"`
	Waiting    int64 `json:"waiting"`
	TargetLoad int64 `json:"target_load"`
}

// ScenarioResult separates completion rate, latency and token throughput per
// scenario step so a slow step cannot hide behind a fast one's averages.
type ScenarioResult struct {
	Name              string       `json:"name"`
	Issued            int64        `json:"issued"`
	Completed         int64        `json:"completed"`
	Failures          int64        `json:"failures"`
	Cancelled         int64        `json:"cancelled"`
	CompletionPercent float64      `json:"completion_percent"`
	Latency           Distribution `json:"latency"`
	TTFT              Distribution `json:"ttft"`
	OutputTokens      int64        `json:"output_tokens"`
	OutputPerSecond   float64      `json:"output_per_second"`
	// InputTokens shows the context each step actually carried. With
	// AccumulateContext on this grows turn by turn, which is what makes a later
	// step slower than an earlier one.
	InputTokens int64       `json:"input_tokens"`
	Samples     *RunSamples `json:"samples,omitempty"`
}

// RunSamples carries the raw measurements a shard collected. A percentile is not
// a linear statistic, so it cannot be merged from per-shard percentiles: the
// Controller pools these samples and computes each distribution exactly once.
// Stripped from the stored result once the merge is done.
type RunSamples struct {
	Latency   []int64 `json:"latency,omitempty"`
	TTFT      []int64 `json:"ttft,omitempty"`
	TTFO      []int64 `json:"ttfo,omitempty"`
	ITL       []int64 `json:"itl,omitempty"`
	TPOT      []int64 `json:"tpot,omitempty"`
	Decimated bool    `json:"decimated,omitempty"`
}

// RunProgress is the live once-a-second snapshot an Agent reports while a shard
// is still running, so the UI can show target load against real activity.
type RunProgress struct {
	ShardID      string  `json:"shard_id"`
	Phase        string  `json:"phase"`
	Second       int64   `json:"second"`
	TargetLoad   int64   `json:"target_load"`
	Active       int64   `json:"active"`
	Waiting      int64   `json:"waiting"`
	Issued       int64   `json:"issued"`
	Completed    int64   `json:"completed"`
	Failures     int64   `json:"failures"`
	Cancelled    int64   `json:"cancelled"`
	Dropped      int64   `json:"dropped"`
	CompletedRPS float64 `json:"completed_rps"`
}

type RunResult struct {
	Successes          int64            `json:"successes"`
	Failures           int64            `json:"failures"`
	P95Millis          int64            `json:"p95_millis"`
	TTFTP95Millis      int64            `json:"ttft_p95_millis"`
	Total              int64            `json:"total"`
	ThroughputRPS      float64          `json:"throughput_rps"`
	Latency            Distribution     `json:"latency"`
	TTFT               Distribution     `json:"ttft"`
	TTFO               Distribution     `json:"ttfo"`
	ITL                Distribution     `json:"itl"`
	TPOT               Distribution     `json:"tpot"`
	Tokens             TokenUsage       `json:"tokens"`
	GoodputPercent     float64          `json:"goodput_percent"`
	DroppedArrivals    int64            `json:"dropped_arrivals"`
	StoppedByGuardrail bool             `json:"stopped_by_guardrail"`
	GuardrailMessage   string           `json:"guardrail_message,omitempty"`
	AgentSessions      int64            `json:"agent_sessions,omitempty"`
	CompletedSessions  int64            `json:"completed_sessions,omitempty"`
	StatusCounts       map[string]int64 `json:"status_counts"`
	Errors             []string         `json:"errors"`
	Timeline           []TimelinePoint  `json:"timeline"`
	LatencyScope       string           `json:"latency_scope,omitempty"`
	// Request lifecycle, kept separate so an unfinished request is never silently
	// dropped: Issued = Successes + HTTPFailures + TransportErrors + Cancelled.
	Issued            int64   `json:"issued"`
	Completed         int64   `json:"completed"`
	Cancelled         int64   `json:"cancelled"`
	HTTPFailures      int64   `json:"http_failures"`
	TransportErrors   int64   `json:"transport_errors"`
	CompletionPercent float64 `json:"completion_percent"`
	// Latency, TTFT, TTFO, ITL and TPOT above are measured over the steady-state
	// window only; SteadySeconds is where it starts and SteadySamples how many
	// successful requests it covers.
	SteadySeconds  int64            `json:"steady_state_seconds"`
	SteadySamples  int64            `json:"steady_state_samples"`
	Scenarios      []ScenarioResult `json:"scenarios,omitempty"`
	DrainedSeconds int64            `json:"drained_seconds,omitempty"`
	Samples        *RunSamples      `json:"samples,omitempty"`
	// MissingUsageResponses counts successful model responses that carried no
	// usage field. Token metrics cannot be trusted for those: ContentChunks is a
	// count of streamed content chunks, which is NOT a token count because a
	// server may pack several tokens into one chunk.
	MissingUsageResponses int64 `json:"missing_usage_responses,omitempty"`
	ContentChunks         int64 `json:"content_chunks,omitempty"`
	OutputLengthPinned    bool  `json:"output_length_pinned"`
	// SamplesDecimated means the sample cap was reached and the retained set was
	// thinned uniformly, so the percentiles are estimates rather than exact.
	SamplesDecimated   bool `json:"samples_decimated,omitempty"`
	ContextAccumulated bool `json:"context_accumulated"`
}

// MonitoringSample is one second of server-side state. Metrics is a map rather
// than fixed fields because an absent metric must stay absent: DCGM profiling
// fields silently fail to collect on some drivers and in some containers, and
// reporting those as 0 would read as "idle GPU" instead of "not measured".
type MonitoringSample struct {
	AtSecond int64              `json:"at_second"`
	Status   string             `json:"status"`
	Backend  string             `json:"backend,omitempty"`
	Message  string             `json:"message,omitempty"`
	Metrics  map[string]float64 `json:"metrics,omitempty"`
}

func (s MonitoringSample) Value(key string) (float64, bool) {
	if s.Metrics == nil {
		return 0, false
	}
	value, ok := s.Metrics[key]
	return value, ok
}

// Engine queueing and KV state. The capacity verdict rests on these three
// together: KV usage alone is high by design, because vLLM deliberately fills
// the cache to maximise batch size.
const (
	MetricRequestsRunning    = "requests_running"
	MetricRequestsWaiting    = "requests_waiting"
	MetricKVCacheUsage       = "kv_cache_usage"
	MetricPreemptionRate     = "preemption_rate"
	MetricQueueTimeP95       = "queue_time_p95_millis"
	MetricPrefillTimeP95     = "prefill_time_p95_millis"
	MetricPrefixCacheHitRate = "prefix_cache_hit_rate"
	MetricCorruptedRate      = "corrupted_requests_rate"
)

// GPU state. GPU utilization is time-based ("was any kernel running") and reads
// 90-100% even at batch size 1, so it must never be read as a capacity ceiling.
// DRAM activity is what actually saturates during decode.
const (
	MetricGPUUtilization       = "gpu_utilization"
	MetricGPUMemoryUsed        = "gpu_memory_used"
	MetricDRAMActive           = "dram_active"
	MetricTensorActive         = "tensor_active"
	MetricSMActive             = "sm_active"
	MetricSMOccupancy          = "sm_occupancy"
	MetricSMClockMHz           = "sm_clock_mhz"
	MetricPowerViolationRate   = "power_violation_rate"
	MetricThermalViolationRate = "thermal_violation_rate"
	MetricXIDErrors            = "xid_errors"
	MetricCPUUtilization       = "cpu_utilization"
	MetricMemoryUsed           = "memory_used"
)

type Run struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	SearchID   string             `json:"search_id,omitempty"`
	Config     RunConfig          `json:"config"`
	Result     RunResult          `json:"result"`
	Monitoring []MonitoringSample `json:"monitoring,omitempty"`
	Progress   []RunProgress      `json:"progress,omitempty"`
	// StartedUnix is when the run began executing, used to align the server-side
	// samples with the client-side timeline.
	StartedUnix int64 `json:"started_unix,omitempty"`
	// DetectedServer is the configuration scraped from the model server itself,
	// kept separate from what the operator entered so a contradiction is visible.
	DetectedServer ServerConfig `json:"detected_server,omitempty"`
}

type AutoSearchStatus string

const (
	AutoSearchRunning   AutoSearchStatus = "running"
	AutoSearchCompleted AutoSearchStatus = "completed"
	AutoSearchCancelled AutoSearchStatus = "cancelled"
)

type AutoSearchConfig struct {
	Run       RunConfig `json:"run"`
	StartLoad int       `json:"start_load"`
	MaxLoad   int       `json:"max_load"`
}

type AutoSearch struct {
	ID              string           `json:"id"`
	Status          AutoSearchStatus `json:"status"`
	Config          AutoSearchConfig `json:"config"`
	RunIDs          []string         `json:"run_ids"`
	NextLoad        int              `json:"next_load"`
	StableLoad      int              `json:"stable_load"`
	FailedLoad      int              `json:"failed_load"`
	RecommendedLoad int              `json:"recommended_load"`
	Message         string           `json:"message"`
	// Ladder is the concurrency sweep planned up front. A single stable/unstable
	// answer cannot locate the knee of the throughput-latency curve; the curve has
	// to be measured, which means running every rung.
	Ladder []int `json:"ladder,omitempty"`
	// Steps is the measured curve, one entry per completed rung.
	Steps []AutoSearchStep `json:"steps,omitempty"`
	// ProvisionLoad is the load to actually run in production: below the knee, so
	// normal variation does not push operations past it.
	ProvisionLoad int `json:"provision_load,omitempty"`
}

// AutoSearchStep is one measured point on the throughput-latency curve.
type AutoSearchStep struct {
	Load               int     `json:"load"`
	RunID              string  `json:"run_id"`
	Stable             bool    `json:"stable"`
	Reason             string  `json:"reason,omitempty"`
	ThroughputRPS      float64 `json:"throughput_rps"`
	OutputTokensPerSec float64 `json:"output_tokens_per_second"`
	TTFTP95Millis      int64   `json:"ttft_p95_millis"`
	TPOTP95Millis      int64   `json:"tpot_p95_millis"`
	LatencyP95Millis   int64   `json:"latency_p95_millis"`
	GoodputPercent     float64 `json:"goodput_percent"`
	CompletionPercent  float64 `json:"completion_percent"`
}

// provisionHeadroomPercent is how far below the knee to advise running. Operating
// at the knee leaves nothing for normal variation, and the curve turns vertical
// just past it.
const provisionHeadroomPercent = 70

type Assignment struct {
	Run    Run    `json:"run"`
	Target Target `json:"target"`
	Shard  Shard  `json:"shard"`
}

type Shard struct {
	ID     string `json:"id"`
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Index  int    `json:"index"`
}
