package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spsp4755/load-observatory/internal/core"
)

type measurements struct {
	mu        sync.Mutex
	successes int64
	failures  int64
	latencies []int64
	ttfts     []int64
}

func Run(ctx context.Context, targetURL string, config core.RunConfig) core.RunResult {
	return RunTarget(ctx, core.Target{Type: core.TargetTypeWeb, URL: targetURL}, config)
}

func RunTarget(ctx context.Context, target core.Target, config core.RunConfig) core.RunResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(config.DurationSeconds)*time.Second)
	defer cancel()

	m := &measurements{}
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
			m.recordFailure()
			return
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.URL, body)
	if err != nil {
		m.recordFailure()
		return
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	ttft := time.Since(started).Milliseconds()
	if err != nil {
		if ctx.Err() == nil {
			m.recordFailure()
		}
		return
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		m.recordFailure()
		return
	}
	m.recordSuccess(time.Since(started).Milliseconds(), ttft)
}

func (m *measurements) recordSuccess(latency, ttft int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.successes++
	m.latencies = append(m.latencies, latency)
	m.ttfts = append(m.ttfts, ttft)
}

func (m *measurements) recordFailure() { m.mu.Lock(); defer m.mu.Unlock(); m.failures++ }

func (m *measurements) result() core.RunResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	return core.RunResult{Successes: m.successes, Failures: m.failures, P95Millis: p95(m.latencies), TTFTP95Millis: p95(m.ttfts)}
}

func p95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	return copyValues[(len(copyValues)*95+99)/100-1]
}
