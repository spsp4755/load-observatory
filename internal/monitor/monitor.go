package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

// candidate is one way to obtain a metric. Names differ between engines and
// between versions of the same engine, so every metric carries a list of
// expressions and the first one that returns a value wins.
type candidate struct {
	backend    string
	expression string
}

type metricQuery struct {
	key        string
	candidates []candidate
}

// Prometheus appends _total to counters on exposition, so counter expressions
// use the exposed name. Where a name changed between versions both are listed:
// vllm:gpu_cache_usage_perc became vllm:kv_cache_usage_perc, and SGLang changed
// its separator from "sglang:" to "sglang_".
var queries = []metricQuery{
	{key: core.MetricRequestsRunning, candidates: []candidate{
		{"vllm", "sum(vllm:num_requests_running)"},
		{"sglang", "sum(sglang:num_running_reqs)"},
		{"sglang", "sum(sglang_num_running_reqs)"},
		{"tgi", "sum(tgi_batch_current_size)"},
	}},
	{key: core.MetricRequestsWaiting, candidates: []candidate{
		{"vllm", "sum(vllm:num_requests_waiting)"},
		{"sglang", "sum(sglang:num_queue_reqs)"},
		{"sglang", "sum(sglang_num_queue_reqs)"},
		{"tgi", "sum(tgi_queue_size)"},
	}},
	{key: core.MetricKVCacheUsage, candidates: []candidate{
		{"vllm", "avg(vllm:kv_cache_usage_perc)"},
		{"vllm", "avg(vllm:gpu_cache_usage_perc)"},
		{"sglang", "avg(sglang:token_usage)"},
		{"sglang", "avg(sglang_token_usage)"},
	}},
	{key: core.MetricPreemptionRate, candidates: []candidate{
		{"vllm", "sum(rate(vllm:num_preemptions_total[1m]))"},
	}},
	{key: core.MetricQueueTimeP95, candidates: []candidate{
		{"vllm", "histogram_quantile(0.95, sum(rate(vllm:request_queue_time_seconds_bucket[1m])) by (le)) * 1000"},
		{"tgi", "histogram_quantile(0.95, sum(rate(tgi_request_queue_duration_bucket[1m])) by (le)) * 1000"},
	}},
	{key: core.MetricPrefillTimeP95, candidates: []candidate{
		{"vllm", "histogram_quantile(0.95, sum(rate(vllm:request_prefill_time_seconds_bucket[1m])) by (le)) * 1000"},
	}},
	{key: core.MetricPrefixCacheHitRate, candidates: []candidate{
		{"vllm", "sum(rate(vllm:prefix_cache_hits_total[2m])) / sum(rate(vllm:prefix_cache_queries_total[2m]))"},
		{"sglang", "avg(sglang:cache_hit_rate)"},
		{"sglang", "avg(sglang_cache_hit_rate)"},
	}},
	{key: core.MetricCorruptedRate, candidates: []candidate{
		{"vllm", "sum(rate(vllm:corrupted_requests_total[1m]))"},
	}},
	// GPU utilization is time-based ("was any kernel running") and reads ~100%
	// even at batch size 1, so it is collected for continuity but never read as a
	// capacity ceiling.
	{key: core.MetricGPUUtilization, candidates: []candidate{{"", "avg(DCGM_FI_DEV_GPU_UTIL)"}}},
	{key: core.MetricGPUMemoryUsed, candidates: []candidate{{"", "100 * sum(DCGM_FI_DEV_FB_USED) / sum(DCGM_FI_DEV_FB_USED + DCGM_FI_DEV_FB_FREE)"}}},
	// DRAM activity is what actually saturates during decode; tensor activity
	// shows prefill dominance. These need the DCGM profiling module and may be
	// absent, which must read as "not measured" rather than as zero.
	{key: core.MetricDRAMActive, candidates: []candidate{{"", "avg(DCGM_FI_PROF_DRAM_ACTIVE)"}}},
	{key: core.MetricTensorActive, candidates: []candidate{{"", "avg(DCGM_FI_PROF_PIPE_TENSOR_ACTIVE)"}}},
	{key: core.MetricSMActive, candidates: []candidate{{"", "avg(DCGM_FI_PROF_SM_ACTIVE)"}}},
	{key: core.MetricSMOccupancy, candidates: []candidate{{"", "avg(DCGM_FI_PROF_SM_OCCUPANCY)"}}},
	{key: core.MetricSMClockMHz, candidates: []candidate{{"", "avg(DCGM_FI_DEV_SM_CLOCK)"}}},
	{key: core.MetricPowerViolationRate, candidates: []candidate{{"", "sum(rate(DCGM_FI_DEV_POWER_VIOLATION[1m]))"}}},
	{key: core.MetricThermalViolationRate, candidates: []candidate{{"", "sum(rate(DCGM_FI_DEV_THERMAL_VIOLATION[1m]))"}}},
	{key: core.MetricXIDErrors, candidates: []candidate{{"", "max(DCGM_FI_DEV_XID_ERRORS)"}}},
	{key: core.MetricCPUUtilization, candidates: []candidate{{"", `100 - avg(rate(node_cpu_seconds_total{mode="idle"}[1m])) * 100`}}},
	{key: core.MetricMemoryUsed, candidates: []candidate{{"", "100 * (node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes) / node_memory_MemTotal_bytes"}}},
}

// absentRetryInterval is how long a metric that matched no candidate is left
// alone. Without it a per-second sample would re-probe every dead name on every
// tick, which for ~20 metrics is a needless burst of queries each second.
const absentRetryInterval = time.Minute

// resolver remembers which candidate answered for each metric. Held behind a
// pointer so Client stays safe to copy.
type resolver struct {
	mu       sync.Mutex
	resolved map[string]candidate
	absent   map[string]time.Time
}

type Client struct {
	url   string
	http  *http.Client
	state *resolver
}

func New(url string) Client {
	return Client{
		url:   url,
		http:  &http.Client{Timeout: 5 * time.Second},
		state: &resolver{resolved: map[string]candidate{}, absent: map[string]time.Time{}},
	}
}

func (c Client) Sample() core.MonitoringSample {
	if c.url == "" {
		return core.MonitoringSample{Status: "unavailable", Message: "Prometheus URL not configured"}
	}
	metrics := map[string]float64{}
	backend := ""
	for _, query := range queries {
		value, from, ok := c.resolve(query)
		if !ok {
			continue
		}
		metrics[query.key] = value
		if from != "" && backend == "" {
			backend = from
		}
	}
	if len(metrics) == 0 {
		return core.MonitoringSample{Status: "unavailable", Message: "Prometheus에서 알려진 지표를 찾지 못했습니다."}
	}
	sample := core.MonitoringSample{Status: "collected", Backend: backend, Metrics: metrics}
	// Report what could not be measured. Silently omitting these would let the
	// verdict read an unmeasured GPU as an idle one.
	if _, ok := metrics[core.MetricRequestsWaiting]; !ok {
		sample.Status = "partial"
		sample.Message = "모델 서버 큐 지표를 찾을 수 없어 포화 여부를 판정할 수 없습니다. vLLM·SGLang·TGI의 /metrics가 Prometheus에 수집되는지 확인하세요."
	} else if _, ok := metrics[core.MetricDRAMActive]; !ok {
		sample.Status = "partial"
		sample.Message = "DCGM 프로파일링 지표(DCGM_FI_PROF_*)를 수집할 수 없습니다. GPU_UTIL만으로는 GPU 포화를 판단할 수 없습니다."
	}
	return sample
}

// resolve returns the metric, trying the candidate that answered last time
// before probing the rest. A metric that matches nothing is retried only
// occasionally, so an engine that starts exporting it later is still picked up.
func (c Client) resolve(query metricQuery) (float64, string, bool) {
	c.state.mu.Lock()
	remembered, hasRemembered := c.state.resolved[query.key]
	retryAt, isAbsent := c.state.absent[query.key]
	c.state.mu.Unlock()

	if hasRemembered {
		if value, err := c.query(remembered.expression); err == nil {
			return value, remembered.backend, true
		}
	} else if isAbsent && time.Now().Before(retryAt) {
		return 0, "", false
	}

	for _, option := range query.candidates {
		value, err := c.query(option.expression)
		if err != nil {
			continue
		}
		c.state.mu.Lock()
		c.state.resolved[query.key] = option
		delete(c.state.absent, query.key)
		c.state.mu.Unlock()
		return value, option.backend, true
	}
	c.state.mu.Lock()
	delete(c.state.resolved, query.key)
	c.state.absent[query.key] = time.Now().Add(absentRetryInterval)
	c.state.mu.Unlock()
	return 0, "", false
}

func (c Client) query(expression string) (float64, error) {
	response, err := c.http.Get(c.url + "/api/v1/query?query=" + url.QueryEscape(expression))
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) != nil || len(payload.Data.Result) == 0 || len(payload.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("metric unavailable")
	}
	var raw string
	if json.Unmarshal(payload.Data.Result[0].Value[1], &raw) != nil {
		return 0, fmt.Errorf("metric unavailable")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	// Prometheus renders an empty histogram_quantile or a 0/0 ratio as NaN.
	if value != value {
		return 0, fmt.Errorf("metric not a number")
	}
	return value, nil
}
