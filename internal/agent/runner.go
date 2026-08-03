package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	sequence     atomic.Int64
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
	sequence, varied := m.nextVariation(config)
	method, body := http.MethodGet, io.Reader(nil)
	requestURL := target.URL
	if varied && target.Type == core.TargetTypeWeb {
		separator := "?"
		if strings.Contains(requestURL, "?") {
			separator = "&"
		}
		requestURL += fmt.Sprintf("%s__lo_run=%s&__lo_request=%d", separator, config.WorkloadID, sequence)
	}
	if target.Type == core.TargetTypeModel || strings.HasSuffix(target.URL, "/v1/chat/completions") {
		method = http.MethodPost
		prompt := config.Prompt
		if varied {
			prompt += fmt.Sprintf("\n\n[Load Observatory variation run=%s request=%d]", config.WorkloadID, sequence)
		}
		requestPayload := struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens     int  `json:"max_tokens"`
			Stream        bool `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}{Model: target.Model, Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{{Role: "user", Content: prompt}}, MaxTokens: config.MaxTokens, Stream: target.Type == core.TargetTypeModel}
		if target.Type == core.TargetTypeModel {
			requestPayload.StreamOptions.IncludeUsage = true
		}
		payload, err := json.Marshal(requestPayload)
		if err != nil {
			m.recordFailure(time.Since(started).Milliseconds(), "encode request")
			return
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		m.recordFailure(time.Since(started).Milliseconds(), "create request")
		return
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if target.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+target.APIKey)
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
		var readErr error
		ttft, usage, readErr = readModelResponse(response, started)
		if readErr != nil {
			if ctx.Err() == nil {
				m.recordFailure(time.Since(started).Milliseconds(), readErr.Error())
			}
			return
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	_ = response.Body.Close()
	latency := time.Since(started).Milliseconds()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		m.recordFailureWithStatus(latency, response.StatusCode, "HTTP "+response.Status)
		return
	}
	m.recordSuccess(latency, ttft, response.StatusCode, usage)
}

func readModelResponse(response *http.Response, started time.Time) (int64, core.TokenUsage, error) {
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		return readModelJSON(response.Body, time.Since(started).Milliseconds())
	}
	var usage core.TokenUsage
	ttft := int64(0)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage modelUsage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			return 0, usage, fmt.Errorf("decode model stream: %w", err)
		}
		if ttft == 0 && len(event.Choices) > 0 && (event.Choices[0].Delta.Content != "" || event.Choices[0].Delta.ReasoningContent != "") {
			ttft = time.Since(started).Milliseconds()
		}
		usage = event.Usage.tokenUsage()
	}
	if err := scanner.Err(); err != nil {
		return 0, usage, fmt.Errorf("read model stream: %w", err)
	}
	if ttft == 0 {
		ttft = time.Since(started).Milliseconds()
	}
	return ttft, usage, nil
}

type modelUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	Details          struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (usage modelUsage) tokenUsage() core.TokenUsage {
	reasoning := usage.ReasoningTokens
	if reasoning == 0 {
		reasoning = usage.Details.ReasoningTokens
	}
	return core.TokenUsage{Prompt: usage.PromptTokens, Completion: usage.CompletionTokens, Reasoning: reasoning}
}

func readModelJSON(body io.Reader, ttft int64) (int64, core.TokenUsage, error) {
	var payload struct {
		Usage modelUsage `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&payload); err != nil && err != io.EOF {
		return 0, core.TokenUsage{}, fmt.Errorf("decode model response: %w", err)
	}
	return ttft, payload.Usage.tokenUsage(), nil
}

func (m *measurements) nextVariation(config core.RunConfig) (int64, bool) {
	sequence := m.sequence.Add(1)
	policy := config.CachePolicy
	if policy == "" {
		policy = core.CachePolicyReuse
	}
	if policy == core.CachePolicyBypass {
		return sequence, true
	}
	if policy == core.CachePolicyReuse {
		return sequence, false
	}
	percent := config.VariationPercent
	if percent == 0 {
		percent = 30
	}
	return sequence, (sequence*37)%100 < int64(percent)
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
