package core

const (
	MaxVUs             = 500
	MaxRPS             = 2000
	MaxDurationSeconds = 60 * 60
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
	ID   string     `json:"id"`
	Name string     `json:"name"`
	Type TargetType `json:"type"`
	URL  string     `json:"url"`
}

type RunConfig struct {
	TargetID        string   `json:"target_id"`
	Mode            LoadMode `json:"mode"`
	VUs             int      `json:"vus"`
	RPS             int      `json:"rps"`
	DurationSeconds int      `json:"duration_seconds"`
}

type RunResult struct {
	Successes     int64 `json:"successes"`
	Failures      int64 `json:"failures"`
	P95Millis     int64 `json:"p95_millis"`
	TTFTP95Millis int64 `json:"ttft_p95_millis"`
}
