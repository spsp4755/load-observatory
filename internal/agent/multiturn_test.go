package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
)

// captureConversations records the messages array of every request.
func captureConversations(t *testing.T, answer string) (*[][]chatMessage, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	var seen [][]chatMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []chatMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		seen = append(seen, request.Messages)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":" + quote(answer) + "}}]}\n\n"))
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"))
	}))
	return &seen, server
}

func quote(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}

func agentConfig(accumulate bool) core.RunConfig {
	return core.RunConfig{
		Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 16, AgentWorkflow: true,
		AccumulateContext: accumulate,
		Scenario: []core.ScenarioTask{
			{Name: "search", Prompt: "STEP ONE", Weight: 1},
			{Name: "edit", Prompt: "STEP TWO", Weight: 1},
			{Name: "test", Prompt: "STEP THREE", Weight: 1},
		},
	}
}

// A real agent session resends every prior turn, so the prompt grows. Without
// that growth the dominant driver of KV cache pressure is unmodelled.
func TestAccumulatedSessionGrowsTheContextEachTurn(t *testing.T) {
	seen, server := captureConversations(t, "ANSWER TEXT")
	defer server.Close()

	RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: server.URL, Model: "m"}, agentConfig(true))

	if len(*seen) < 3 {
		t.Fatalf("expected at least 3 turns, got %d", len(*seen))
	}
	first, second, third := (*seen)[0], (*seen)[1], (*seen)[2]
	if len(first) != 1 {
		t.Fatalf("turn 1 should carry only the first prompt, got %d messages", len(first))
	}
	if len(second) != 3 || second[0].Role != "user" || second[1].Role != "assistant" || second[2].Role != "user" {
		t.Fatalf("turn 2 did not carry the prior exchange: %+v", second)
	}
	if second[1].Content != "ANSWER TEXT" {
		t.Fatalf("the model's own answer was not carried forward: %q", second[1].Content)
	}
	if len(third) != 5 {
		t.Fatalf("turn 3 should carry two prior exchanges plus the new prompt, got %d messages", len(third))
	}
	// Each turn must be strictly larger than the last.
	if !(size(first) < size(second) && size(second) < size(third)) {
		t.Fatalf("context did not grow: %d, %d, %d", size(first), size(second), size(third))
	}
}

// Off by default: accumulating changes what is measured, so it must be explicit.
func TestSessionWithoutAccumulationSendsEachTurnAlone(t *testing.T) {
	seen, server := captureConversations(t, "ANSWER")
	defer server.Close()

	RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: server.URL, Model: "m"}, agentConfig(false))

	for i, conversation := range *seen {
		if len(conversation) != 1 {
			t.Fatalf("turn %d carried %d messages without accumulation enabled", i+1, len(conversation))
		}
	}
}

func TestResultRecordsWhetherContextWasAccumulated(t *testing.T) {
	_, server := captureConversations(t, "A")
	defer server.Close()
	target := core.Target{Type: core.TargetTypeModel, URL: server.URL, Model: "m"}

	if result := RunTarget(context.Background(), target, agentConfig(true)); !result.ContextAccumulated {
		t.Fatal("accumulated run did not record it")
	}
	if result := RunTarget(context.Background(), target, agentConfig(false)); result.ContextAccumulated {
		t.Fatal("non-accumulated run claimed accumulation")
	}
}

// Per-scenario input tokens are what make the context growth visible in results.
func TestScenarioResultsCarryInputTokens(t *testing.T) {
	_, server := captureConversations(t, "A")
	defer server.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: server.URL, Model: "m"}, agentConfig(true))
	if len(result.Scenarios) == 0 {
		t.Fatal("no scenarios reported")
	}
	for _, scenario := range result.Scenarios {
		if scenario.InputTokens == 0 {
			t.Fatalf("scenario %q reports no input tokens, so context growth is invisible", scenario.Name)
		}
	}
}

// The accumulated conversation must stay bounded.
func TestAccumulatedHistoryIsTrimmedToItsCap(t *testing.T) {
	huge := strings.Repeat("x", maxAnswerChars)
	history := []chatMessage{
		{Role: "user", Content: "one"}, {Role: "assistant", Content: huge},
		{Role: "user", Content: "two"}, {Role: "assistant", Content: huge},
		{Role: "user", Content: "three"}, {Role: "assistant", Content: huge},
	}
	trimmed := trimHistory(history)
	total := 0
	for _, message := range trimmed {
		total += len(message.Content)
	}
	if len(trimmed) >= len(history) {
		t.Fatalf("history was not trimmed: %d messages", len(trimmed))
	}
	if total > maxAnswerChars*2 {
		t.Fatalf("trimmed history still holds %d chars", total)
	}
	// The most recent turn must survive.
	if trimmed[len(trimmed)-2].Content != "three" {
		t.Fatalf("trimming dropped the newest turn: %+v", trimmed[len(trimmed)-2])
	}
}

// Goodput must count SLO-meeting requests against everything that finished, so a
// server that sheds the hard requests cannot score well by failing them.
func TestGoodputCountsErrorsInItsDenominator(t *testing.T) {
	var count int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		odd := count%2 == 1
		mu.Unlock()
		if odd {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
			return
		}
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: server.URL, Model: "m"},
		core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, MaxTokens: 8, Prompt: "p", MaxP95Millis: 600000})
	if result.Failures == 0 || result.Successes == 0 {
		t.Fatalf("expected a mix of successes and failures: %+v", result)
	}
	// Roughly half the finished requests errored, so goodput cannot be ~100%.
	if result.GoodputPercent > 75 {
		t.Fatalf("goodput %.1f%% ignores the errored half of the run", result.GoodputPercent)
	}
}

func size(messages []chatMessage) int {
	total := 0
	for _, message := range messages {
		total += len(message.Content)
	}
	return total
}
