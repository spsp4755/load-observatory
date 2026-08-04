package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spsp4755/load-observatory/internal/core"
)

// vLLM's prefix caching hashes leading token blocks, so a nonce appended to the
// end leaves the whole prompt cached and the request never measures prefill.
// The nonce must come first.
func TestCacheBypassNoncePrecedesThePromptSoPrefixCachingCannotHit(t *testing.T) {
	prompts := make(chan string, 4)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		select {
		case prompts <- request.Messages[0].Content:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":4,"completion_tokens":2}}`))
	}))
	defer target.Close()

	RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"},
		core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "SHARED PROMPT BODY", MaxTokens: 4, CachePolicy: core.CachePolicyBypass})
	close(prompts)

	var seen []string
	for prompt := range prompts {
		seen = append(seen, prompt)
		if !strings.HasPrefix(prompt, "[LO-") {
			t.Fatalf("nonce is not at the front of the prompt, prefix caching will still hit: %q", prompt)
		}
		if !strings.HasSuffix(prompt, "SHARED PROMPT BODY") {
			t.Fatalf("prompt body was not preserved after the nonce: %q", prompt)
		}
	}
	if len(seen) < 2 {
		t.Fatalf("need at least two requests to compare prefixes, got %d", len(seen))
	}
	// vLLM reuses only complete leading blocks of block_size tokens (16 by
	// default), so the requests must already differ well inside the first block.
	// A handful of characters is a safe proxy for "within the first 16 tokens".
	for i := 1; i < len(seen); i++ {
		if commonPrefixLen(seen[0], seen[i]) > 8 {
			t.Fatalf("requests share too long a leading prefix, the first token block would still cache:\n  %q\n  %q", seen[0], seen[i])
		}
	}
}

func commonPrefixLen(a, b string) int {
	limit := min(len(a), len(b))
	for i := range limit {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

// ignore_eos is a vLLM/SGLang extension. It must be opt-in, because a server
// that rejects unknown fields would otherwise fail every request.
func TestIgnoreEOSIsSentOnlyWhenEnabled(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		fields := make(chan map[string]any, 2)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			select {
			case fields <- request:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
		}))

		result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"},
			core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "test", MaxTokens: 64, IgnoreEOS: pinned})
		target.Close()
		close(fields)

		request := <-fields
		_, present := request["ignore_eos"]
		if present != pinned {
			t.Fatalf("IgnoreEOS=%t but ignore_eos present=%t", pinned, present)
		}
		if pinned && request["min_tokens"] != float64(64) {
			t.Fatalf("pinned run did not floor the output length: min_tokens=%v", request["min_tokens"])
		}
		if result.OutputLengthPinned != pinned {
			t.Fatalf("result does not record whether output length was pinned: got %t want %t", result.OutputLengthPinned, pinned)
		}
	}
}

// A response with no usage field cannot be turned into a token count. Chunk
// counts are not token counts, so the result must say the usage was missing.
func TestMissingUsageIsReportedRatherThanSilentlyZero(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for range 3 {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"tok \"}}]}\n\n"))
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"},
		core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "test", MaxTokens: 8})
	if result.Successes == 0 {
		t.Fatal("no request succeeded")
	}
	if result.MissingUsageResponses == 0 {
		t.Fatalf("server returned no usage but the result does not say so: %+v", result)
	}
	if result.ContentChunks == 0 {
		t.Fatal("content chunks were not counted as the labelled fallback")
	}
	if result.Tokens.Completion != 0 {
		t.Fatalf("chunks must not be reported as tokens: completion=%d", result.Tokens.Completion)
	}
}

// A server that does report usage must not be flagged.
func TestReportedUsageIsNotFlaggedAsMissing(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n"))
	}))
	defer target.Close()

	result := RunTarget(context.Background(), core.Target{Type: core.TargetTypeModel, URL: target.URL, Model: "model"},
		core.RunConfig{Mode: core.LoadModeVU, VUs: 1, DurationSeconds: 1, Prompt: "test", MaxTokens: 8})
	if result.MissingUsageResponses != 0 {
		t.Fatalf("usage was reported but flagged missing: %+v", result)
	}
	if result.Tokens.Completion == 0 {
		t.Fatal("reported usage was not accumulated")
	}
}

// The Controller needs the raw samples to pool percentiles across shards.
func TestResultCarriesRawSamplesForPooledPercentiles(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	result := Run(context.Background(), target.URL, core.RunConfig{Mode: core.LoadModeVU, VUs: 2, DurationSeconds: 1})
	if result.Samples == nil {
		t.Fatal("result carries no raw samples, so shard percentiles cannot be pooled")
	}
	if int64(len(result.Samples.Latency)) != result.SteadySamples {
		t.Fatalf("latency samples %d do not match steady samples %d", len(result.Samples.Latency), result.SteadySamples)
	}
}

// sampleSet must stay bounded while keeping coverage across the whole run, not
// just its beginning.
func TestSampleSetDecimatesUniformlyInsteadOfTruncating(t *testing.T) {
	var set sampleSet
	total := maxSamplesPerMetric * 4
	for i := range total {
		set.add(int64(i))
	}
	if set.len() > maxSamplesPerMetric {
		t.Fatalf("sample set grew past the cap: %d", set.len())
	}
	if !set.decimated {
		t.Fatal("expected the set to report that it decimated")
	}
	last := set.values[set.len()-1]
	if last < int64(total)*3/4 {
		t.Fatalf("late samples were dropped, coverage is biased to the run start: last=%d of %d", last, total)
	}
	if set.values[0] > int64(total)/4 {
		t.Fatalf("early samples were dropped entirely: first=%d of %d", set.values[0], total)
	}
}

// Two shards each count their own requests from 1, so the nonce must also depend
// on the shard or both emit identical prompts and hit each other's prefix cache.
func TestVariationPrefixDiffersAcrossShardsAtTheSameSequence(t *testing.T) {
	shardA := variationPrefix("run-2-shard-3", 55)
	shardB := variationPrefix("run-2-shard-4", 55)
	if shardA == shardB {
		t.Fatal("two shards produced the same nonce at the same sequence")
	}
	if n := commonPrefixLen(shardA, shardB); n > 8 {
		t.Fatalf("shard nonces share %d leading chars, the first token block would still cache:\n  %q\n  %q", n, shardA, shardB)
	}
	// Consecutive sequences within one shard must diverge just as early.
	first, second := variationPrefix("run-2-shard-3", 100), variationPrefix("run-2-shard-3", 101)
	if n := commonPrefixLen(first, second); n > 8 {
		t.Fatalf("consecutive nonces share %d leading chars:\n  %q\n  %q", n, first, second)
	}
}
