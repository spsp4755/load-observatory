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

type TargetType string

const (
	TargetTypeWeb   TargetType = "web"
	TargetTypeModel TargetType = "model"
)

type Target struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Type  TargetType `json:"type"`
	URL   string     `json:"url"`
	Model string     `json:"model,omitempty"`
}

type RunConfig struct {
	TargetID        string   `json:"target_id"`
	Mode            LoadMode `json:"mode"`
	VUs             int      `json:"vus"`
	RPS             int      `json:"rps"`
	DurationSeconds int      `json:"duration_seconds"`
	Prompt          string   `json:"prompt"`
	MaxTokens       int      `json:"max_tokens"`
	MaxErrorPercent float64  `json:"max_error_percent"`
	MaxP95Millis    int64    `json:"max_p95_millis"`
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
}

type RunResult struct {
	Successes     int64            `json:"successes"`
	Failures      int64            `json:"failures"`
	P95Millis     int64            `json:"p95_millis"`
	TTFTP95Millis int64            `json:"ttft_p95_millis"`
	Total         int64            `json:"total"`
	ThroughputRPS float64          `json:"throughput_rps"`
	Latency       Distribution     `json:"latency"`
	TTFT          Distribution     `json:"ttft"`
	Tokens        TokenUsage       `json:"tokens"`
	StatusCounts  map[string]int64 `json:"status_counts"`
	Errors        []string         `json:"errors"`
	Timeline      []TimelinePoint  `json:"timeline"`
}

type Run struct {
	ID     string    `json:"id"`
	Status string    `json:"status"`
	Config RunConfig `json:"config"`
	Result RunResult `json:"result"`
}

type Assignment struct {
	Run    Run    `json:"run"`
	Target Target `json:"target"`
}
