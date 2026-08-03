# Detailed Load Results Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide capacity verdicts and diagnostic evidence for each load-test run.

**Architecture:** Extend the current Go agent's in-memory measurements and `core.RunResult`; the controller and memory store return the richer run unchanged. React renders the verdict and diagnostics for the active or selected historical run.

**Tech Stack:** Go standard library, React 19, Vite, Node built-in test runner.

## Global Constraints

- Support generic HTTP targets and OpenAI-compatible `/v1/chat/completions` only.
- Add no dependencies or external services.
- Keep data in the current memory store; records disappear after controller restart.
- Bound retained errors and timeline points.
- Missing or malformed OpenAI `usage` remains an unavailable metric, not a request failure.

---

### Task 1: Aggregate detailed request metrics

**Files:**
- Modify: `internal/core/types.go`
- Modify: `internal/agent/runner.go`
- Test: `internal/agent/runner_test.go`

**Interfaces:** Produces `RunResult` total, throughput, latency and TTFT distributions, status counts, errors, and per-second timeline points.

- [ ] **Step 1: Write the failing aggregation test**

```go
if result.Total != 2 || result.Latency.P99Millis != 30 || result.StatusCounts["500"] != 1 {
    t.Fatalf("unexpected aggregate: %+v", result)
}
```

- [ ] **Step 2: Run it and verify failure**

Run: `go test ./internal/agent -run TestRunTargetAggregatesDetailedMetrics -v`

Expected: FAIL because detailed fields do not exist.

- [ ] **Step 3: Add minimal bounded measurement types and recording**

```go
type Distribution struct { MinMillis, AvgMillis, P50Millis, P95Millis, P99Millis, MaxMillis int64 }
type TimelinePoint struct { Second int64 `json:"second"`; Requests, Successes, Failures int64 `json:"requests","successes","failures"`; P95Millis int64 `json:"p95_millis"` }
```

Record status codes, truncated error text, samples, and one-second buckets. Keep at most 20 errors and 120 timeline points.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/agent ./internal/core -v`

Expected: PASS.

- [ ] **Step 5: Commit**

Run: `git add internal/core/types.go internal/agent/runner.go internal/agent/runner_test.go; git commit -m "feat: collect detailed load metrics"`

### Task 2: Decode model usage and initialize verdict thresholds

**Files:**
- Modify: `internal/agent/runner.go`
- Modify: `internal/agent/runner_test.go`
- Modify: `internal/controller/server.go`
- Test: `internal/controller/server_test.go`

**Interfaces:** Consumes Task 1 result fields. Produces model token totals/TPS and run configuration fields `MaxErrorPercent` and `MaxP95Millis` defaulting to `2` and `2000`.

- [ ] **Step 1: Write failing usage/default tests**

```go
if result.Tokens.Completion != 12 || result.Tokens.OutputPerSecond <= 0 { t.Fatal("usage missing") }
if run.Config.MaxErrorPercent != 2 || run.Config.MaxP95Millis != 2000 { t.Fatal("defaults missing") }
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/agent ./internal/controller -run 'TestRunTargetCollectsUsage|TestCreateRunDefaultsVerdictThresholds' -v`

Expected: FAIL because usage and defaults are absent.

- [ ] **Step 3: Implement bounded usage decoding and defaults**

Use `json.NewDecoder(io.LimitReader(response.Body, 1<<20))` only after a successful model response. Extract `usage.prompt_tokens`, `completion_tokens`, and optional `reasoning_tokens`; keep generic response handling unchanged. Apply threshold defaults in `createRun`.

- [ ] **Step 4: Run focused tests**

Run: `go test ./internal/agent ./internal/controller -v`

Expected: PASS.

- [ ] **Step 5: Commit**

Run: `git add internal/agent internal/controller; git commit -m "feat: report model token usage and thresholds"`

### Task 3: Render the capacity dashboard

**Files:**
- Modify: `web/src/run-form.js`
- Modify: `web/src/App.jsx`
- Modify: `web/src/App.css`
- Test: `web/test/run-form.test.js`

**Interfaces:** Consumes detailed `RunResult`; produces exported `getVerdict(run)` with `{ status: "stable" | "at-risk" | "pending", recommendation: string }`.

- [ ] **Step 1: Write the failing verdict test**

```js
assert.equal(getVerdict({ status: "completed", config: { vus: 10, max_error_percent: 2, max_p95_millis: 2000 }, result: { failures: 0, total: 10, latency: { p95_millis: 100 } } }).status, "stable");
```

- [ ] **Step 2: Run and verify failure**

Run: `npm.cmd test --prefix web`

Expected: FAIL because `getVerdict` is absent.

- [ ] **Step 3: Implement verdict and result detail**

Add threshold inputs; render recommendation, success rate, achieved RPS, latency/TTFT distributions, token/TPS cards, time buckets, status counts, and errors. Use `—` for unavailable usage.

- [ ] **Step 4: Run UI verification**

Run: `npm.cmd test --prefix web; node web/node_modules/vite/bin/vite.js build --config web/vite.config.js`

Expected: PASS.

- [ ] **Step 5: Commit**

Run: `git add web; git commit -m "feat: show capacity verdict and detailed metrics"`

### Task 4: Add selected record details and release verification

**Files:**
- Modify: `web/src/App.jsx`
- Modify: `web/src/App.css`
- Test: `internal/controller/server_test.go`

**Interfaces:** Consumes the existing `GET /api/runs` list; selecting a run displays target, model, configuration, prompt preview, verdict, and all detailed fields.

- [ ] **Step 1: Write a failing serialization test**

```go
if !bytes.Contains(response.Body.Bytes(), []byte(`"timeline"`)) { t.Fatal("detailed result not serialized") }
```

- [ ] **Step 2: Run and verify failure**

Run: `go test ./internal/controller -run TestListRunsIncludesDetailedResult -v`

Expected: FAIL until the detailed fixture exists.

- [ ] **Step 3: Implement record selection**

Keep the current refresh action and show selected run detail without a new backend endpoint.

- [ ] **Step 4: Verify and test LM Studio**

Run: `go test ./...; go vet ./...; npm.cmd test --prefix web; node web/node_modules/vite/bin/vite.js build --config web/vite.config.js; powershell -ExecutionPolicy Bypass -File deploy/check-manifest.ps1`

Expected: all commands pass. Run the supplied LM Studio model with 1 VU, 5 seconds, and `max_tokens=32`; verify a verdict, records selection, and monitoring navigation.

- [ ] **Step 5: Commit and push**

Run: `git add internal web; git commit -m "feat: add detailed load-test records"; git push origin main`
