package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

// ponytail: one timeline point per second for the whole allowed run length, and
// at most maxSecondSamples latencies per point. Raise if per-second percentiles
// need more resolution than that at very high RPS.
const (
	maxTimelinePoints = core.MaxDurationSeconds + 600
	maxSecondSamples  = 1000
	// maxSamplesPerMetric bounds what one shard ships to the Controller for
	// pooled percentile computation. ITL produces one sample per token, so this
	// is reachable; sampleSet decimates uniformly rather than truncating.
	maxSamplesPerMetric = 100000
)

// sampleSet keeps a bounded, evenly spread subset of the values it is given, so
// the Controller can pool raw samples across shards and compute each percentile
// exactly once without unbounded memory. When full it drops every other retained
// value and halves its acceptance rate, which keeps coverage uniform across the
// whole run instead of biased toward its beginning.
type sampleSet struct {
	values    []int64
	stride    int64
	seen      int64
	decimated bool
}

func (s *sampleSet) add(value int64) {
	if s.stride == 0 {
		s.stride = 1
	}
	s.seen++
	if s.seen%s.stride != 0 {
		return
	}
	s.values = append(s.values, value)
	if len(s.values) >= maxSamplesPerMetric {
		kept := s.values[:0]
		for i := 1; i < len(s.values); i += 2 {
			kept = append(kept, s.values[i])
		}
		s.values = kept
		s.stride *= 2
		s.decimated = true
	}
}

func (s *sampleSet) addAll(values []int64) {
	for _, value := range values {
		s.add(value)
	}
}

func (s *sampleSet) len() int { return len(s.values) }

const (
	phaseWarmup   = "warmup"
	phaseLoad     = "load"
	phaseDrain    = "drain"
	phaseCooldown = "cooldown"
	phaseDone     = "done"
)

type measurements struct {
	mu          sync.Mutex
	started     time.Time
	steadyAfter time.Duration

	// Request lifecycle, tracked separately so a request that never finished is
	// visible instead of vanishing from the totals.
	issued, cancelled, httpFailures, transportErrors int64
	successes, goodput, dropped                      int64
	sessions, completedSessions                      int64

	// Samples from the steady-state window only. These feed every reported
	// distribution, so ramp-up and warmup cannot flatter the percentiles.
	latencies, ttfts, ttfos, itls, tpots sampleSet
	steadySamples                        int64

	// Token accounting trust: a response with no usage field cannot be counted.
	missingUsage, contentChunks int64

	statusCounts  map[string]int64
	errors        []string
	tokens        core.TokenUsage
	timeline      map[int64]*timelineMeasurement
	scenarios     map[string]*scenarioMeasurement
	scenarioOrder []string

	active, waiting, targetLoad atomic.Int64
	phase                       atomic.Value
	drainedSeconds              atomic.Int64

	sequence        atomic.Int64
	journeySequence atomic.Int64
	stop            context.CancelFunc
	stopped         bool
	guardrail       string
}
type timelineMeasurement struct {
	issued, completed, successes, failures, cancelled int64
	active, waiting, targetLoad                       int64
	latencies                                         []int64
}
type scenarioMeasurement struct {
	issued, completed, failures, cancelled int64
	latencies, ttfts                       sampleSet
	outputTokens, inputTokens              int64
}
type modelTiming struct {
	ttft, ttfo, tpot int64
	itls             []int64
	chunks           int64
	usageReported    bool
	// answer is the assistant text, kept only so a multi-turn session can carry
	// it into the next request and grow the context the way a real session does.
	answer string
}

// chatMessage is one turn of an OpenAI-compatible conversation.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// maxAnswerChars bounds what one accumulated conversation can carry, so a long
// session cannot grow without limit in the Agent's memory.
const maxAnswerChars = 200000

// attempt carries the per-request bookkeeping from issue time to record time.
type attempt struct {
	startedAt time.Time
	scenario  string
	steady    bool
}

func Run(ctx context.Context, targetURL string, config core.RunConfig) core.RunResult {
	return RunTarget(ctx, core.Target{Type: core.TargetTypeWeb, URL: targetURL}, config)
}

func RunTarget(ctx context.Context, target core.Target, config core.RunConfig) core.RunResult {
	return RunTargetWithProgress(ctx, target, config, nil)
}

// RunTargetWithProgress runs the load and, when report is non-nil, calls it once
// a second with a live snapshot of target load against real activity.
func RunTargetWithProgress(ctx context.Context, target core.Target, config core.RunConfig, report func(core.RunProgress)) core.RunResult {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	m := &measurements{started: time.Now(), statusCounts: map[string]int64{}, timeline: map[int64]*timelineMeasurement{}, scenarios: map[string]*scenarioMeasurement{}}
	m.steadyAfter = time.Duration(config.SteadyStateSeconds) * time.Second
	m.setPhase(phaseWarmup)
	m.stop = cancelRun
	client := &http.Client{}
	for range config.WarmupRequests {
		doRequest(runCtx, client, target, config, m, nil)
	}
	m.reset()

	sampling := make(chan struct{})
	go m.sampleLoop(sampling, report)
	defer close(sampling)

	stages := config.Stages
	if len(stages) == 0 {
		stages = []core.LoadStage{{DurationSeconds: config.DurationSeconds, TargetLoad: load(config)}}
	}
	m.setPhase(phaseLoad)
	for _, stage := range stages {
		if runCtx.Err() != nil {
			break
		}
		stageConfig := config
		stageConfig.DurationSeconds = stage.DurationSeconds
		if config.Mode == core.LoadModeRPS {
			stageConfig.RPS = stage.TargetLoad
		} else {
			stageConfig.VUs = stage.TargetLoad
		}
		m.targetLoad.Store(int64(stage.TargetLoad))
		runStage(runCtx, client, target, stageConfig, m)
	}
	m.targetLoad.Store(0)
	if config.CooldownSeconds > 0 && runCtx.Err() == nil {
		m.setPhase(phaseCooldown)
		select {
		case <-time.After(time.Duration(config.CooldownSeconds) * time.Second):
		case <-runCtx.Done():
		}
	}
	m.setPhase(phaseDone)
	return m.result(config)
}

// runStage issues load for the stage duration, then stops issuing and lets the
// requests already in flight finish for up to DrainSeconds. Only requests still
// unfinished after the drain window are cancelled, and those are counted as
// cancelled rather than dropped or counted as failures.
func runStage(runCtx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	issueCtx, stopIssue := context.WithTimeout(runCtx, time.Duration(config.DurationSeconds)*time.Second)
	defer stopIssue()
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(runCtx))
	defer cancelRequests()
	stageDone := make(chan struct{})
	defer close(stageDone)
	// A run cancelled by the user or a guardrail must not wait out the drain.
	go func() {
		select {
		case <-runCtx.Done():
			cancelRequests()
		case <-stageDone:
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if config.Mode == core.LoadModeRPS {
			runRPS(issueCtx, requestCtx, client, target, config, m)
		} else {
			runVUs(issueCtx, requestCtx, client, target, config, m)
		}
	}()

	select {
	case <-done:
		return
	case <-issueCtx.Done():
	}
	if runCtx.Err() != nil || config.DrainSeconds <= 0 {
		cancelRequests()
		<-done
		return
	}
	m.setPhase(phaseDrain)
	drainStarted := time.Now()
	select {
	case <-done:
	case <-time.After(time.Duration(config.DrainSeconds) * time.Second):
		cancelRequests()
		<-done
	case <-runCtx.Done():
		cancelRequests()
		<-done
	}
	m.drainedSeconds.Add(int64(time.Since(drainStarted).Seconds()))
	m.setPhase(phaseLoad)
}

func load(config core.RunConfig) int {
	if config.Mode == core.LoadModeRPS {
		return config.RPS
	}
	return config.VUs
}
func runVUs(issueCtx, requestCtx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	var wg sync.WaitGroup
	for range config.VUs {
		wg.Add(1)
		go func() { defer wg.Done(); runWorker(issueCtx, requestCtx, client, target, config, m) }()
	}
	wg.Wait()
}
func runRPS(issueCtx, requestCtx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
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
		case <-issueCtx.Done():
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
			go func() {
				defer wg.Done()
				defer func() { <-inFlight }()
				runJourney(issueCtx, requestCtx, client, target, config, m)
			}()
		}
	}
}
func runWorker(issueCtx, requestCtx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	if len(config.Journeys) > 0 {
		for issueCtx.Err() == nil {
			runJourney(issueCtx, requestCtx, client, target, config, m)
		}
		return
	}
	if config.AgentWorkflow {
		for issueCtx.Err() == nil {
			runAgentSession(issueCtx, requestCtx, client, target, config, m)
		}
		return
	}
	for issueCtx.Err() == nil {
		wait, _ := doRequest(requestCtx, client, target, config, m, nil)
		m.think(issueCtx, wait)
	}
}

func runJourney(issueCtx, requestCtx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	if len(config.Journeys) == 0 {
		if config.AgentWorkflow {
			runAgentSession(issueCtx, requestCtx, client, target, config, m)
		} else {
			doRequest(requestCtx, client, target, config, m, nil)
		}
		return
	}
	journey := m.nextJourney(config.Journeys)
	journeyConfig := config
	journeyConfig.AgentWorkflow = journey.AgentWorkflow
	journeyConfig.Scenario = journey.Scenario
	if journey.AgentWorkflow {
		runAgentSession(issueCtx, requestCtx, client, target, journeyConfig, m)
		return
	}
	wait, _ := doRequest(requestCtx, client, target, journeyConfig, m, nil)
	m.think(issueCtx, wait)
}

func runAgentSession(issueCtx, requestCtx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements) {
	if len(config.Scenario) == 0 {
		return
	}
	m.startSession()
	before := m.successCount()
	// A real chat or agent session carries every prior turn, so the prompt grows
	// each turn. That growth is the dominant driver of KV cache pressure and of
	// TTFT growth, so a session that does not accumulate under-states both.
	var history []chatMessage
	for _, task := range config.Scenario {
		if issueCtx.Err() != nil {
			return
		}
		step := config
		step.Prompt = task.Prompt
		// Keep the task as the only scenario entry so the step is attributed to
		// its own name in the per-scenario breakdown.
		step.Scenario = []core.ScenarioTask{task}
		if task.MaxTokens > 0 {
			step.MaxTokens = task.MaxTokens
		}
		wait, answer := doRequest(requestCtx, client, target, step, m, history)
		if config.AccumulateContext {
			history = append(history,
				chatMessage{Role: "user", Content: task.Prompt},
				chatMessage{Role: "assistant", Content: answer})
			history = trimHistory(history)
		}
		if task.ThinkTimeMillis > 0 {
			wait = time.Duration(task.ThinkTimeMillis) * time.Millisecond
		}
		if !m.think(issueCtx, wait) {
			return
		}
	}
	m.completeSession(m.successCount()-before == int64(len(config.Scenario)))
}

func doRequest(ctx context.Context, client *http.Client, target core.Target, config core.RunConfig, m *measurements, history []chatMessage) (time.Duration, string) {
	sequence, varied, name, prompt, think, maxTokens := m.nextWorkload(config)
	call := m.beginAttempt(name)
	m.active.Add(1)
	defer m.active.Add(-1)
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
			prompt = variationPrefix(config.WorkloadID, sequence) + prompt
		}
		messages := append(append([]chatMessage(nil), history...), chatMessage{Role: "user", Content: prompt})
		request := map[string]any{"model": target.Model, "messages": messages, "max_tokens": maxTokens, "stream": target.Type == core.TargetTypeModel, "stream_options": map[string]bool{"include_usage": true}}
		if config.IgnoreEOS {
			request["ignore_eos"] = true
			request["min_tokens"] = maxTokens
		}
		payload, err := json.Marshal(request)
		if err != nil {
			m.recordTransportError(call, "encode request")
			return 0, ""
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		m.recordTransportError(call, "create request")
		return 0, ""
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	if target.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+target.APIKey)
	}
	response, err := client.Do(request)
	if err != nil {
		m.recordAborted(call, ctx, err)
		return 0, ""
	}
	timing := modelTiming{ttft: time.Since(call.startedAt).Milliseconds()}
	usage := core.TokenUsage{}
	if target.Type == core.TargetTypeModel && response.StatusCode >= 200 && response.StatusCode < 400 {
		timing, usage, err = readModelResponse(response, call.startedAt)
		if err != nil {
			_ = response.Body.Close()
			m.recordAborted(call, ctx, err)
			return 0, ""
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	_ = response.Body.Close()
	latency := time.Since(call.startedAt).Milliseconds()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		m.recordHTTPFailure(call, latency, response.StatusCode, "HTTP "+response.Status)
		return 0, ""
	}
	m.recordSuccess(call, latency, timing, response.StatusCode, usage, config)
	return think, timing.answer
}

func readModelResponse(response *http.Response, started time.Time) (modelTiming, core.TokenUsage, error) {
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		usage, err := readModelJSON(response.Body)
		elapsed := time.Since(started).Milliseconds()
		return modelTiming{ttft: elapsed, ttfo: elapsed, usageReported: usage.Completion > 0}, usage, err
	}
	var usage core.TokenUsage
	timing := modelTiming{}
	var lastChunk time.Time
	var answer strings.Builder
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
				timing.chunks++
				// Only the final answer carries into the next turn; reasoning
				// content is not part of the conversation a client would resend.
				if delta.Content != "" && answer.Len() < maxAnswerChars {
					answer.WriteString(delta.Content)
				}
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
		if reported := event.Usage.tokenUsage(); reported.Completion > 0 || reported.Prompt > 0 {
			usage = reported
			timing.usageReported = true
		}
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
	timing.answer = answer.String()
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

// trimHistory drops the oldest turns once the accumulated conversation exceeds
// what one session may carry, keeping the most recent context. The cap exists so
// a long session cannot grow without limit in the Agent's memory.
func trimHistory(history []chatMessage) []chatMessage {
	total := 0
	for _, message := range history {
		total += len(message.Content)
	}
	for total > maxAnswerChars && len(history) > 2 {
		total -= len(history[0].Content) + len(history[1].Content)
		history = history[2:]
	}
	return history
}

// variationPrefix builds the cache-bypass nonce that goes in FRONT of the
// prompt. Prefix caching (on by default in vLLM since v0.6.0) reuses only
// *complete* leading blocks of block_size tokens (16 by default), so:
//   - a trailing nonce leaves the whole prompt cached and never measures prefill
//   - a leading nonce with a constant preamble still lets the first block match
//   - two shards counting from 1 with the same run id emit identical nonces and
//     hit each other's cache
//
// So the hash of (workload, sequence) leads, putting unique bytes at token ~2.
// The readable part follows for log correlation.
func variationPrefix(workloadID string, sequence int64) string {
	digest := fnv.New32a()
	_, _ = fmt.Fprintf(digest, "%s#%d", workloadID, sequence)
	return fmt.Sprintf("[LO-%08x %s#%d]\n\n", digest.Sum32(), workloadID, sequence)
}

func (m *measurements) nextWorkload(config core.RunConfig) (int64, bool, string, string, time.Duration, int) {
	sequence := m.sequence.Add(1)
	name := ""
	prompt := config.Prompt
	think := time.Duration(0)
	maxTokens := config.MaxTokens
	total := 0
	for _, task := range config.Scenario {
		total += task.Weight
	}
	if total > 0 {
		pick := int(sequence % int64(total))
		for _, task := range config.Scenario {
			pick -= task.Weight
			if pick < 0 {
				name = task.Name
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
	return sequence, varied, name, prompt, think, maxTokens
}

func (m *measurements) nextJourney(journeys []core.UserJourney) core.UserJourney {
	sequence := m.journeySequence.Add(1)
	total := 0
	for _, journey := range journeys {
		total += journey.Weight
	}
	if total == 0 {
		return journeys[0]
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

// think blocks for the user's think time, counting the worker as waiting rather
// than active. It reports false when the issue window closed during the wait.
func (m *measurements) think(issueCtx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		return issueCtx.Err() == nil
	}
	m.waiting.Add(1)
	defer m.waiting.Add(-1)
	select {
	case <-time.After(wait):
		return true
	case <-issueCtx.Done():
		return false
	}
}

func (m *measurements) setPhase(phase string) { m.phase.Store(phase) }
func (m *measurements) currentPhase() string {
	if phase, ok := m.phase.Load().(string); ok {
		return phase
	}
	return phaseLoad
}

// sampleLoop records the once-a-second gauges and pushes the live snapshot.
func (m *measurements) sampleLoop(done <-chan struct{}, report func(core.RunProgress)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			snapshot := m.sampleGauges()
			if report != nil {
				report(snapshot)
			}
		}
	}
}

func (m *measurements) sampleGauges() core.RunProgress {
	active, waiting, target := m.active.Load(), m.waiting.Load(), m.targetLoad.Load()
	m.mu.Lock()
	defer m.mu.Unlock()
	elapsed := time.Since(m.started).Seconds()
	second := int64(elapsed)
	if point := m.timelineAtLocked(second); point != nil {
		point.active = max(point.active, active)
		point.waiting = max(point.waiting, waiting)
		point.targetLoad = max(point.targetLoad, target)
	}
	completed := m.successes + m.httpFailures
	progress := core.RunProgress{Phase: m.currentPhase(), Second: second, TargetLoad: target, Active: active, Waiting: waiting, Issued: m.issued, Completed: completed, Failures: m.httpFailures + m.transportErrors, Cancelled: m.cancelled, Dropped: m.dropped}
	if elapsed > 0 {
		progress.CompletedRPS = float64(completed) / elapsed
	}
	return progress
}

func (m *measurements) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = time.Now()
	m.issued = 0
	m.cancelled = 0
	m.httpFailures = 0
	m.transportErrors = 0
	m.successes = 0
	m.goodput = 0
	m.dropped = 0
	m.sessions = 0
	m.completedSessions = 0
	m.stopped = false
	m.guardrail = ""
	m.latencies = sampleSet{}
	m.ttfts = sampleSet{}
	m.ttfos = sampleSet{}
	m.itls = sampleSet{}
	m.tpots = sampleSet{}
	m.steadySamples = 0
	m.missingUsage = 0
	m.contentChunks = 0
	m.statusCounts = map[string]int64{}
	m.errors = nil
	m.tokens = core.TokenUsage{}
	m.timeline = map[int64]*timelineMeasurement{}
	m.scenarios = map[string]*scenarioMeasurement{}
	m.scenarioOrder = nil
}

// beginAttempt records that a request was issued and decides up front whether it
// belongs to the steady-state window, so a request started during ramp-up stays
// excluded from the percentiles even if it finishes much later.
func (m *measurements) beginAttempt(scenario string) attempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	offset := now.Sub(m.started)
	m.issued++
	if point := m.timelineAtLocked(int64(offset.Seconds())); point != nil {
		point.issued++
	}
	if scenario != "" {
		m.scenarioLocked(scenario).issued++
	}
	return attempt{startedAt: now, scenario: scenario, steady: offset >= m.steadyAfter}
}

func (m *measurements) scenarioLocked(name string) *scenarioMeasurement {
	entry := m.scenarios[name]
	if entry == nil {
		entry = &scenarioMeasurement{}
		m.scenarios[name] = entry
		m.scenarioOrder = append(m.scenarioOrder, name)
	}
	return entry
}

func (m *measurements) recordSuccess(call attempt, latency int64, timing modelTiming, status int, usage core.TokenUsage, config core.RunConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.successes++
	if call.steady {
		m.steadySamples++
		m.latencies.add(latency)
		m.ttfts.add(timing.ttft)
		m.ttfos.add(timing.ttfo)
		m.itls.addAll(timing.itls)
		if timing.tpot > 0 {
			m.tpots.add(timing.tpot)
		}
	}
	m.statusCounts[fmt.Sprintf("%d", status)]++
	m.tokens.Prompt += usage.Prompt
	m.tokens.Completion += usage.Completion
	m.tokens.Reasoning += usage.Reasoning
	m.contentChunks += timing.chunks
	if !timing.usageReported {
		m.missingUsage++
	}
	if meetsSLO(latency, timing, usage, config) {
		m.goodput++
	}
	if call.scenario != "" {
		entry := m.scenarioLocked(call.scenario)
		entry.completed++
		entry.outputTokens += usage.Completion
		entry.inputTokens += usage.Prompt
		if call.steady {
			entry.latencies.add(latency)
			entry.ttfts.add(timing.ttft)
		}
	}
	m.recordTimelineLocked(latency, timelineSuccess)
	m.maybeStopLocked(config)
}

func (m *measurements) maybeStopLocked(config core.RunConfig) {
	if m.stopped || m.steadySamples < 10 {
		return
	}
	message := ""
	if config.MaxP95Millis > 0 && p95(m.latencies.values) > config.MaxP95Millis {
		message = "E2E P95 SLO exceeded"
	}
	if message == "" && config.MaxTTFTP95Millis > 0 && p95(m.ttfts.values) > config.MaxTTFTP95Millis {
		message = "TTFT P95 SLO exceeded"
	}
	if message == "" && config.MaxTTPOTP95Millis > 0 && p95(m.tpots.values) > config.MaxTTPOTP95Millis {
		message = "TPOT P95 SLO exceeded"
	}
	if finished := m.successes + m.httpFailures + m.transportErrors; message == "" && config.MinGoodputPercent > 0 && finished > 0 && float64(m.goodput)*100/float64(finished) < config.MinGoodputPercent {
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

// recordAborted separates a request the run itself cut short from a genuine
// transport failure of the target.
func (m *measurements) recordAborted(call attempt, ctx context.Context, err error) {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		m.recordCancelled(call)
		return
	}
	m.recordTransportError(call, err.Error())
}

func (m *measurements) recordCancelled(call attempt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelled++
	if call.scenario != "" {
		m.scenarioLocked(call.scenario).cancelled++
	}
	m.recordTimelineLocked(time.Since(call.startedAt).Milliseconds(), timelineCancelled)
}

func (m *measurements) recordTransportError(call attempt, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transportErrors++
	m.appendErrorLocked(message)
	if call.scenario != "" {
		m.scenarioLocked(call.scenario).failures++
	}
	m.recordTimelineLocked(time.Since(call.startedAt).Milliseconds(), timelineFailure)
}

func (m *measurements) recordHTTPFailure(call attempt, latency int64, status int, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpFailures++
	m.statusCounts[fmt.Sprintf("%d", status)]++
	m.appendErrorLocked(message)
	if call.scenario != "" {
		entry := m.scenarioLocked(call.scenario)
		entry.failures++
		entry.completed++
	}
	m.recordTimelineLocked(latency, timelineFailure)
}

func (m *measurements) appendErrorLocked(message string) {
	if len(m.errors) >= 20 {
		return
	}
	if len(message) > 180 {
		message = message[:180]
	}
	m.errors = append(m.errors, message)
}

type timelineOutcome int

const (
	timelineSuccess timelineOutcome = iota
	timelineFailure
	timelineCancelled
)

func (m *measurements) timelineAtLocked(second int64) *timelineMeasurement {
	point := m.timeline[second]
	if point == nil {
		if len(m.timeline) >= maxTimelinePoints {
			return nil
		}
		point = &timelineMeasurement{}
		m.timeline[second] = point
	}
	return point
}

func (m *measurements) recordTimelineLocked(latency int64, outcome timelineOutcome) {
	point := m.timelineAtLocked(int64(time.Since(m.started).Seconds()))
	if point == nil {
		return
	}
	switch outcome {
	case timelineSuccess:
		point.successes++
		point.completed++
	case timelineFailure:
		point.failures++
		point.completed++
	case timelineCancelled:
		// A cancelled request never finished, so its partial elapsed time would
		// drag the per-second percentile below what the target actually served.
		point.cancelled++
		return
	}
	if len(point.latencies) < maxSecondSamples {
		point.latencies = append(point.latencies, latency)
	}
}

func (m *measurements) result(config core.RunConfig) core.RunResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	completed := m.successes + m.httpFailures
	failures := m.httpFailures + m.transportErrors
	total := m.successes + failures
	timeline := make([]core.TimelinePoint, 0, len(m.timeline))
	for second, point := range m.timeline {
		timeline = append(timeline, core.TimelinePoint{
			Second: second, Requests: point.successes + point.failures, Successes: point.successes, Failures: point.failures,
			P95Millis: p95(point.latencies), Issued: point.issued, Completed: point.completed, Cancelled: point.cancelled,
			Active: point.active, Waiting: point.waiting, TargetLoad: point.targetLoad,
		})
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Second < timeline[j].Second })
	tokens := m.tokens
	elapsed := time.Since(m.started).Seconds()
	if elapsed > 0 {
		tokens.OutputPerSecond = float64(tokens.Completion) / elapsed
	}
	// Goodput counts SLO-meeting requests against every request that finished,
	// errors included. Dividing by successes alone would let a server that sheds
	// load under pressure score well precisely because it failed the hard ones.
	goodput := float64(0)
	if total > 0 {
		goodput = float64(m.goodput) * 100 / float64(total)
	}
	completionPercent := float64(0)
	if m.issued > 0 {
		completionPercent = float64(completed) * 100 / float64(m.issued)
	}
	scenarios := make([]core.ScenarioResult, 0, len(m.scenarioOrder))
	for _, name := range m.scenarioOrder {
		entry := m.scenarios[name]
		item := core.ScenarioResult{Name: name, Issued: entry.issued, Completed: entry.completed, Failures: entry.failures, Cancelled: entry.cancelled, Latency: distribution(entry.latencies.values), TTFT: distribution(entry.ttfts.values), OutputTokens: entry.outputTokens, InputTokens: entry.inputTokens}
		if entry.issued > 0 {
			item.CompletionPercent = float64(entry.completed) * 100 / float64(entry.issued)
		}
		if elapsed > 0 {
			item.OutputPerSecond = float64(entry.outputTokens) / elapsed
		}
		if entry.latencies.len() > 0 || entry.ttfts.len() > 0 {
			item.Samples = &core.RunSamples{Latency: entry.latencies.values, TTFT: entry.ttfts.values, Decimated: entry.latencies.decimated || entry.ttfts.decimated}
		}
		scenarios = append(scenarios, item)
	}
	var samples *core.RunSamples
	if m.latencies.len() > 0 || m.ttfts.len() > 0 || m.itls.len() > 0 {
		samples = &core.RunSamples{
			Latency: m.latencies.values, TTFT: m.ttfts.values, TTFO: m.ttfos.values,
			ITL: m.itls.values, TPOT: m.tpots.values,
			Decimated: m.latencies.decimated || m.ttfts.decimated || m.ttfos.decimated || m.itls.decimated || m.tpots.decimated,
		}
	}
	return core.RunResult{
		Successes: m.successes, Failures: failures, P95Millis: p95(m.latencies.values), TTFTP95Millis: p95(m.ttfts.values), Total: total,
		ThroughputRPS: float64(total) / elapsed, Latency: distribution(m.latencies.values), TTFT: distribution(m.ttfts.values),
		TTFO: distribution(m.ttfos.values), ITL: distribution(m.itls.values), TPOT: distribution(m.tpots.values), Tokens: tokens,
		GoodputPercent: goodput, DroppedArrivals: m.dropped, StoppedByGuardrail: m.stopped, GuardrailMessage: m.guardrail,
		AgentSessions: m.sessions, CompletedSessions: m.completedSessions, StatusCounts: m.statusCounts, Errors: m.errors,
		Timeline: timeline, Issued: m.issued, Completed: completed, Cancelled: m.cancelled, HTTPFailures: m.httpFailures,
		TransportErrors: m.transportErrors, CompletionPercent: completionPercent,
		SteadySeconds: int64(m.steadyAfter.Seconds()), SteadySamples: m.steadySamples, Scenarios: scenarios,
		DrainedSeconds: m.drainedSeconds.Load(), Samples: samples,
		MissingUsageResponses: m.missingUsage, ContentChunks: m.contentChunks, OutputLengthPinned: config.IgnoreEOS, ContextAccumulated: config.AccumulateContext,
	}
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
