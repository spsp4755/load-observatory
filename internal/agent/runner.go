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
	mu                                    sync.Mutex
	started                               time.Time
	successes, failures, goodput, dropped int64
	sessions, completedSessions           int64
	latencies, ttfts, ttfos, itls, tpots  []int64
	statusCounts                          map[string]int64
	errors                                []string
	tokens                                core.TokenUsage
	timeline                              map[int64]*timelineMeasurement
	sequence                              atomic.Int64
	journeySequence                       atomic.Int64
	stop                                  context.CancelFunc
	stopped                               bool
	guardrail                             string
}
type timelineMeasurement struct {
	requests, successes, failures int64
	latencies                     []int64
}
type modelTiming struct {
	ttft, ttfo, tpot int64
	itls             []int64
}

func Run(ctx context.Context, targetURL string, config core.RunConfig) core.RunResult {
	return RunTarget(ctx, core.Target{Type: core.TargetTypeWeb, URL: targetURL}, config)
}

func RunTarget(ctx context.Context, target core.Target, config core.RunConfig) core.RunResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := &measurements{started: time.Now(), statusCounts: map[string]int64{}, timeline: map[int64]*timelineMeasurement{}}
	m.stop = cancel
	client := &http.Client{}
	for range config.WarmupRequests {
		doRequest(ctx, client, target, config, m)
	}
	m.reset()
	stages := config.Stages
	if len(stages) == 0 {
		stages = []core.LoadStage{{DurationSeconds: config.DurationSeconds, TargetLoad: load(config)}}
	}
	for _, stage := range stages {
		if ctx.Err() != nil {
			break
		}
		stageConfig := config
		stageConfig.DurationSeconds = stage.DurationSeconds
		if config.Mode == core.LoadModeRPS {
			stageConfig.RPS = stage.TargetLoad
		} else {
			stageConfig.VUs = stage.TargetLoad
		}
		stageCtx, stop := context.WithTimeout(ctx, time.Duration(stage.DurationSeconds)*time.Second)
		if config.Mode == core.LoadModeRPS {
			runRPS(stageCtx, client, target, stageConfig, m)
		} else {
			runVUs(stageCtx, client, target, stageConfig, m)
		}
		stop()
	}
	if config.CooldownSeconds > 0 && ctx.Err() == nil {
		select {
		case <-time.After(time.Duration(config.CooldownSeconds) * time.Second):
		case <-ctx.Done():
		}
	}
	return m.result()
}
func load(config core.RunConfig) int {
	if config.Mode == core.LoadModeRPS {
		return config.RPS
	}
	return config.VUs
}
func runVUs(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	var wg sync.WaitGroup
	for range config.VUs {
		wg.Add(1)
		go func() { defer wg.Done(); runWorker(ctx, client, target, config, m) }()
	}
	wg.Wait()
}
func runRPS(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	interval := time.Second / time.Duration(config.RPS)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var wg sync.WaitGroup
	limit := config.MaxInFlight
	if limit == 0 {
		limit = core.MaxVUs
	}
	inFlight := make(chan struct{}, limit)
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-ticker.C:
			select {
			case inFlight <- struct{}{}:
			default:
				m.recordDropped()
				continue
			}
			wg.Add(1)
			go func() { defer wg.Done(); defer func() { <-inFlight }(); runJourney(ctx, client, target, config, m) }()
		}
	}
}
func runWorker(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	if len(config.Journeys) > 0 {
		for ctx.Err() == nil {
			runJourney(ctx, client, target, config, m)
		}
		return
	}
	if config.AgentWorkflow {
		for ctx.Err() == nil {
			runAgentSession(ctx, client, target, config, m)
		}
		return
	}
	for ctx.Err() == nil {
		if wait := doRequest(ctx, client, target, config, m); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
			}
		}
	}
}

func runJourney(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	if len(config.Journeys) == 0 {
		if config.AgentWorkflow {
			runAgentSession(ctx, client, target, config, m)
		} else {
			doRequest(ctx, client, target, config, m)
		}
		return
	}
	journey := m.nextJourney(config.Journeys)
	journeyConfig := config
	journeyConfig.AgentWorkflow = journey.AgentWorkflow
	journeyConfig.Scenario = journey.Scenario
	if journey.AgentWorkflow {
		runAgentSession(ctx, client, target, journeyConfig, m)
		return
	}
	if wait := doRequest(ctx, client, target, journeyConfig, m); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
		}
	}
}

func runAgentSession(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	if len(config.Scenario) == 0 {
		return
	}
	m.startSession()
	before := m.successCount()
	for _, task := range config.Scenario {
		if ctx.Err() != nil {
			return
		}
		step := config
		step.Prompt = task.Prompt
		step.Scenario = nil
		if task.MaxTokens > 0 {
			step.MaxTokens = task.MaxTokens
		}
		wait := doRequest(ctx, client, target, step, m)
		if task.ThinkTimeMillis > 0 {
			wait = time.Duration(task.ThinkTimeMillis) * time.Millisecond
		}
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
		}
	}
	m.completeSession(m.successCount()-before == int64(len(config.Scenario)))
}

func doRequest(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) time.Duration {
	started := time.Now()
	sequence, varied, prompt, think, maxTokens := m.nextWorkload(config)
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
		if varied {
			prompt += fmt.Sprintf("\n\n[Load Observatory variation run=%s request=%d]", config.WorkloadID, sequence)
		}
		payload, err := json.Marshal(map[string]any{"model": target.Model, "messages": []map[string]string{{"role": "user", "content": prompt}}, "max_tokens": maxTokens, "stream": target.Type == core.TargetTypeModel, "stream_options": map[string]bool{"include_usage": true}})
		if err != nil {
			m.recordFailure(time.Since(started).Milliseconds(), "encode request")
			return 0
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		m.recordFailure(time.Since(started).Milliseconds(), "create request")
		return 0
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if target.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() == nil {
			m.recordFailure(time.Since(started).Milliseconds(), err.Error())
		}
		return 0
	}
	timing := modelTiming{ttft: time.Since(started).Milliseconds()}
	usage := core.TokenUsage{}
	if target.Type == core.TargetTypeModel && response.StatusCode >= 200 && response.StatusCode < 400 {
		timing, usage, err = readModelResponse(response, started)
		if err != nil {
			_ = response.Body.Close()
			if ctx.Err() == nil {
				m.recordFailure(time.Since(started).Milliseconds(), err.Error())
			}
			return 0
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	_ = response.Body.Close()
	latency := time.Since(started).Milliseconds()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		m.recordFailureWithStatus(latency, response.StatusCode, "HTTP "+response.Status)
		return 0
	}
	m.recordSuccess(latency, timing, response.StatusCode, usage, config)
	return think
}

func readModelResponse(response *http.Response, started time.Time) (modelTiming, core.TokenUsage, error) {
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		usage, err := readModelJSON(response.Body)
		elapsed := time.Since(started).Milliseconds()
		return modelTiming{ttft: elapsed, ttfo: elapsed}, usage, err
	}
	var usage core.TokenUsage
	timing := modelTiming{}
	var lastChunk time.Time
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
			return timing, usage, fmt.Errorf("decode model stream: %w", err)
		}
		now := time.Now()
		if len(event.Choices) > 0 {
			delta := event.Choices[0].Delta
			if delta.Content != "" || delta.ReasoningContent != "" {
				if timing.ttft == 0 {
					timing.ttft = now.Sub(started).Milliseconds()
				}
				if !lastChunk.IsZero() {
					timing.itls = append(timing.itls, now.Sub(lastChunk).Milliseconds())
				}
				lastChunk = now
				if timing.ttfo == 0 && delta.Content != "" {
					timing.ttfo = now.Sub(started).Milliseconds()
				}
			}
		}
		usage = event.Usage.tokenUsage()
	}
	if err := scanner.Err(); err != nil {
		return timing, usage, fmt.Errorf("read model stream: %w", err)
	}
	if timing.ttft == 0 {
		timing.ttft = time.Since(started).Milliseconds()
	}
	if timing.ttfo == 0 {
		timing.ttfo = timing.ttft
	}
	if usage.Completion > 1 {
		timing.tpot = (time.Since(started).Milliseconds() - timing.ttfo) / (usage.Completion - 1)
	}
	if timing.tpot == 0 && len(timing.itls) > 0 {
		timing.tpot = average(timing.itls)
	}
	return timing, usage, nil
}

type modelUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	Details          struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u modelUsage) tokenUsage() core.TokenUsage {
	r := u.ReasoningTokens
	if r == 0 {
		r = u.Details.ReasoningTokens
	}
	return core.TokenUsage{Prompt: u.PromptTokens, Completion: u.CompletionTokens, Reasoning: r}
}
func readModelJSON(body io.Reader) (core.TokenUsage, error) {
	var payload struct {
		Usage modelUsage `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&payload); err != nil && err != io.EOF {
		return core.TokenUsage{}, fmt.Errorf("decode model response: %w", err)
	}
	return payload.Usage.tokenUsage(), nil
}

func (m *measurements) nextWorkload(config core.RunConfig) (int64, bool, string, time.Duration, int) {
	sequence := m.sequence.Add(1)
	prompt := config.Prompt
	think := time.Duration(0)
	maxTokens := config.MaxTokens
	if len(config.Scenario) > 0 {
		total := 0
		for _, task := range config.Scenario {
			total += task.Weight
		}
		pick := int(sequence % int64(total))
		for _, task := range config.Scenario {
			pick -= task.Weight
			if pick < 0 {
				prompt = task.Prompt
				think = time.Duration(task.ThinkTimeMillis) * time.Millisecond
				if task.MaxTokens > 0 {
					maxTokens = task.MaxTokens
				}
				break
			}
		}
	}
	policy := config.CachePolicy
	if policy == "" {
		policy = core.CachePolicyReuse
	}
	varied := policy == core.CachePolicyBypass
	if policy == core.CachePolicyMixed {
		percent := config.VariationPercent
		if percent == 0 {
			percent = 30
		}
		varied = (sequence*37)%100 < int64(percent)
	}
	return sequence, varied, prompt, think, maxTokens
}

func (m *measurements) nextJourney(journeys []core.UserJourney) core.UserJourney {
	sequence := m.journeySequence.Add(1)
	total := 0
	for _, journey := range journeys {
		total += journey.Weight
	}
	pick := int(sequence % int64(total))
	for _, journey := range journeys {
		pick -= journey.Weight
		if pick < 0 {
			return journey
		}
	}
	return journeys[0]
}
func (m *measurements) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = time.Now()
	m.successes = 0
	m.failures = 0
	m.goodput = 0
	m.dropped = 0
	m.sessions = 0
	m.completedSessions = 0
	m.stopped = false
	m.guardrail = ""
	m.latencies = nil
	m.ttfts = nil
	m.ttfos = nil
	m.itls = nil
	m.tpots = nil
	m.statusCounts = map[string]int64{}
	m.errors = nil
	m.tokens = core.TokenUsage{}
	m.timeline = map[int64]*timelineMeasurement{}
}
func (m *measurements) recordSuccess(latency int64, timing modelTiming, status int, usage core.TokenUsage, config core.RunConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.successes++
	m.latencies = append(m.latencies, latency)
	m.ttfts = append(m.ttfts, timing.ttft)
	m.ttfos = append(m.ttfos, timing.ttfo)
	m.itls = append(m.itls, timing.itls...)
	if timing.tpot > 0 {
		m.tpots = append(m.tpots, timing.tpot)
	}
	m.statusCounts[fmt.Sprintf("%d", status)]++
	m.tokens.Prompt += usage.Prompt
	m.tokens.Completion += usage.Completion
	m.tokens.Reasoning += usage.Reasoning
	if meetsSLO(latency, timing, usage, config) {
		m.goodput++
	}
	m.recordTimeline(latency, true)
	m.maybeStopLocked(config)
}

func (m *measurements) maybeStopLocked(config core.RunConfig) {
	if m.stopped || m.successes < 10 {
		return
	}
	message := ""
	if config.MaxP95Millis > 0 && p95(m.latencies) > config.MaxP95Millis {
		message = "E2E P95 SLO exceeded"
	}
	if message == "" && config.MaxTTFTP95Millis > 0 && p95(m.ttfts) > config.MaxTTFTP95Millis {
		message = "TTFT P95 SLO exceeded"
	}
	if message == "" && config.MaxTTPOTP95Millis > 0 && p95(m.tpots) > config.MaxTTPOTP95Millis {
		message = "TPOT P95 SLO exceeded"
	}
	if message == "" && config.MinGoodputPercent > 0 && float64(m.goodput)*100/float64(m.successes) < config.MinGoodputPercent {
		message = "goodput SLO not met"
	}
	if message != "" {
		m.stopped = true
		m.guardrail = message
		if m.stop != nil {
			m.stop()
		}
	}
}
func meetsSLO(latency int64, timing modelTiming, usage core.TokenUsage, config core.RunConfig) bool {
	if config.MaxP95Millis > 0 && latency > config.MaxP95Millis || config.MaxTTFTP95Millis > 0 && timing.ttft > config.MaxTTFTP95Millis || config.MaxTTPOTP95Millis > 0 && timing.tpot > config.MaxTTPOTP95Millis {
		return false
	}
	if config.MinOutputTokensPerSecond > 0 && latency > 0 && float64(usage.Completion)*1000/float64(latency) < config.MinOutputTokensPerSecond {
		return false
	}
	return true
}
func (m *measurements) recordDropped() { m.mu.Lock(); defer m.mu.Unlock(); m.dropped++ }
func (m *measurements) startSession()  { m.mu.Lock(); defer m.mu.Unlock(); m.sessions++ }
func (m *measurements) completeSession(completed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if completed {
		m.completedSessions++
	}
}
func (m *measurements) successCount() int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.successes }
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
	goodput := float64(0)
	if m.successes > 0 {
		goodput = float64(m.goodput) * 100 / float64(m.successes)
	}
	return core.RunResult{Successes: m.successes, Failures: m.failures, P95Millis: p95(m.latencies), TTFTP95Millis: p95(m.ttfts), Total: total, ThroughputRPS: float64(total) / elapsed, Latency: distribution(m.latencies), TTFT: distribution(m.ttfts), TTFO: distribution(m.ttfos), ITL: distribution(m.itls), TPOT: distribution(m.tpots), Tokens: tokens, GoodputPercent: goodput, DroppedArrivals: m.dropped, StoppedByGuardrail: m.stopped, GuardrailMessage: m.guardrail, AgentSessions: m.sessions, CompletedSessions: m.completedSessions, StatusCounts: m.statusCounts, Errors: m.errors, Timeline: timeline}
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
	return core.Distribution{MinMillis: copyValues[0], AvgMillis: average(copyValues), P50Millis: percentile(copyValues, 50), P95Millis: percentile(copyValues, 95), P99Millis: percentile(copyValues, 99), MaxMillis: copyValues[len(copyValues)-1]}
}
func average(values []int64) int64 {
	var sum int64
	for _, v := range values {
		sum += v
	}
	if len(values) == 0 {
		return 0
	}
	return sum / int64(len(values))
}
func percentile(sorted []int64, percentage int) int64 {
	return sorted[(len(sorted)*percentage+99)/100-1]
}
