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
}

type MonitoringSample struct {
	Status         string  `json:"status"`
	GPUUtilization float64 `json:"gpu_utilization"`
	GPUMemoryUsed  float64 `json:"gpu_memory_used"`
	CPUUtilization float64 `json:"cpu_utilization"`
	MemoryUsed     float64 `json:"memory_used"`
	Message        string  `json:"message,omitempty"`
}

type Run struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	SearchID   string             `json:"search_id,omitempty"`
	Config     RunConfig          `json:"config"`
	Result     RunResult          `json:"result"`
	Monitoring []MonitoringSample `json:"monitoring,omitempty"`
	Progress   []RunProgress      `json:"progress,omitempty"`
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
}

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
