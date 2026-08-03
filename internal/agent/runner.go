package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

type measurements struct {
	mu           sync.Mutex
	started      time.Time
	successes    int64
	failures     int64
	latencies    []int64
	ttfts        []int64
	statusCounts map[string]int64
	errors       []string
	tokens       core.TokenUsage
	timeline     map[int64]*timelineMeasurement
}

type timelineMeasurement struct {
	requests, successes, failures int64
	latencies                     []int64
}

func Run(ctx context.Context, targetURL string, config core.RunConfig) core.RunResult {
	return RunTarget(ctx, core.Target{Type: core.TargetTypeWeb, URL: targetURL}, config)
}

func RunTarget(ctx context.Context, target core.Target, config core.RunConfig) core.RunResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(config.DurationSeconds)*time.Second)
	defer cancel()

	m := &measurements{started: time.Now(), statusCounts: map[string]int64{}, timeline: map[int64]*timelineMeasurement{}}
	client := &http.Client{}
	if config.Mode == core.LoadModeRPS {
		runRPS(ctx, client, target, config, m)
	} else {
		var workers sync.WaitGroup
		for range config.VUs {
			workers.Add(1)
			go func() { defer workers.Done(); runWorker(ctx, client, target, config, m) }()
		}
		workers.Wait()
	}
	return m.result()
}

func runRPS(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	interval := time.Second / time.Duration(config.RPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var workers sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return
		case <-ticker.C:
			workers.Add(1)
			go func() { defer workers.Done(); doRequest(ctx, client, target, config, m) }()
		}
	}
}

func runWorker(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	for ctx.Err() == nil {
		doRequest(ctx, client, target, config, m)
	}
}

func doRequest(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	started := time.Now()
	method, body := http.MethodGet, io.Reader(nil)
	if target.Type == core.TargetTypeModel || strings.HasSuffix(target.URL, "/v1/chat/completions") {
		method = http.MethodPost
		payload, err := json.Marshal(struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}{Model: target.Model, Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{{Role: "user", Content: config.Prompt}}, MaxTokens: config.MaxTokens})
		if err != nil {
			m.recordFailure(time.Since(started).Milliseconds(), "encode request")
			return
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.URL, body)
	if err != nil {
		m.recordFailure(time.Since(started).Milliseconds(), "create request")
		return
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	ttft := time.Since(started).Milliseconds()
	if err != nil {
		if ctx.Err() == nil {
			m.recordFailure(time.Since(started).Milliseconds(), err.Error())
		}
		return
	}
	usage := core.TokenUsage{}
	if target.Type == core.TargetTypeModel && response.StatusCode >= 200 && response.StatusCode < 400 {
		var payload struct {
			Usage struct {
				PromptTokens            int64 `json:"prompt_tokens"`
				CompletionTokens        int64 `json:"completion_tokens"`
				ReasoningTokens         int64 `json:"reasoning_tokens"`
				CompletionTokensDetails struct {
					ReasoningTokens int64 `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
			} `json:"usage"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload)
		usage.Prompt, usage.Completion, usage.Reasoning = payload.Usage.PromptTokens, payload.Usage.CompletionTokens, payload.Usage.ReasoningTokens
		if usage.Reasoning == 0 {
			usage.Reasoning = payload.Usage.CompletionTokensDetails.ReasoningTokens
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		m.recordFailureWithStatus(time.Since(started).Milliseconds(), response.StatusCode, "HTTP "+response.Status)
		return
	}
	m.recordSuccess(time.Since(started).Milliseconds(), ttft, response.StatusCode, usage)
}

func (m *measurements) recordSuccess(latency, ttft int64, status int, usage core.TokenUsage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.successes++
	m.latencies = append(m.latencies, latency)
	m.ttfts = append(m.ttfts, ttft)
	m.statusCounts[fmt.Sprintf("%d", status)]++
	m.tokens.Prompt += usage.Prompt
	m.tokens.Completion += usage.Completion
	m.tokens.Reasoning += usage.Reasoning
	m.recordTimeline(latency, true)
}

func (m *measurements) recordFailure(latency int64, message string) {
	m.recordFailureWithStatus(latency, 0, message)
}

func (m *measurements) recordFailureWithStatus(latency int64, status int, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures++
	if status > 0 {
		m.statusCounts[fmt.Sprintf("%d", status)]++
	}
	if len(m.errors) < 20 {
		if len(message) > 180 {
			message = message[:180]
		}
		m.errors = append(m.errors, message)
	}
	m.recordTimeline(latency, false)
}

func (m *measurements) recordTimeline(latency int64, success bool) {
	second := int64(time.Since(m.started).Seconds())
	point := m.timeline[second]
	if point == nil {
		if len(m.timeline) >= 120 {
			return
		}
		point = &timelineMeasurement{}
		m.timeline[second] = point
	}
	point.requests++
	point.latencies = append(point.latencies, latency)
	if success {
		point.successes++
	} else {
		point.failures++
	}
}

func (m *measurements) result() core.RunResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := m.successes + m.failures
	timeline := make([]core.TimelinePoint, 0, len(m.timeline))
	for second, point := range m.timeline {
		timeline = append(timeline, core.TimelinePoint{Second: second, Requests: point.requests, Successes: point.successes, Failures: point.failures, P95Millis: p95(point.latencies)})
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Second < timeline[j].Second })
	tokens := m.tokens
	elapsed := time.Since(m.started).Seconds()
	if elapsed > 0 {
		tokens.OutputPerSecond = float64(tokens.Completion) / elapsed
	}
	return core.RunResult{Successes: m.successes, Failures: m.failures, P95Millis: p95(m.latencies), TTFTP95Millis: p95(m.ttfts), Total: total, ThroughputRPS: float64(total) / elapsed, Latency: distribution(m.latencies), TTFT: distribution(m.ttfts), Tokens: tokens, StatusCounts: m.statusCounts, Errors: m.errors, Timeline: timeline}
}

func p95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	return copyValues[(len(copyValues)*95+99)/100-1]
}

func distribution(values []int64) core.Distribution {
	if len(values) == 0 {
		return core.Distribution{}
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	var sum int64
	for _, value := range copyValues {
		sum += value
	}
	return core.Distribution{MinMillis: copyValues[0], AvgMillis: sum / int64(len(copyValues)), P50Millis: percentile(copyValues, 50), P95Millis: percentile(copyValues, 95), P99Millis: percentile(copyValues, 99), MaxMillis: copyValues[len(copyValues)-1]}
}

func percentile(sorted []int64, percentage int) int64 {
	return sorted[(len(sorted)*percentage+99)/100-1]
}
