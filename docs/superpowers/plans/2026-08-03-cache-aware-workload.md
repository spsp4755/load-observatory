# Cache-aware workload implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a load run mix repeatable and cache-busting requests, and retain the selected policy with its result.

**Architecture:** Add a small cache policy and variation percentage to `core.RunConfig`. The existing Agent derives a deterministic sequence number for each request and transforms only its own outgoing prompt or URL; no target-wide cache purge or service-side command is issued. The React form submits and displays the stored values.

**Tech Stack:** Go standard library, React/Vite.

## Global Constraints

- Keep the existing OpenAI-compatible `/v1/chat/completions` contract.
- Default to mixed traffic: 70% repeatable and 30% varied.
- Do not add dependencies or affect requests not created by Load Observatory.

---

### Task 1: Persist cache-workload choices

**Files:**
- Modify: `internal/core/types.go`, `internal/core/target.go`, `internal/core/target_test.go`

- [ ] Write a failing validation test for an unknown cache policy.
- [ ] Run `go test ./internal/core` and observe the missing validation failure.
- [ ] Add `CachePolicy` (`mixed`, `reuse`, `bypass`) and `VariationPercent` to `RunConfig`, default/validate them in the existing shared validator.
- [ ] Run `go test ./internal/core`.

### Task 2: Vary only Observatory-created requests

**Files:**
- Modify: `internal/agent/runner.go`, `internal/agent/runner_test.go`

- [ ] Write failing tests asserting a bypass model payload contains a unique workload nonce and a bypass web request contains `__lo_run`/`__lo_request` query values.
- [ ] Run `go test ./internal/agent` and observe the assertions fail.
- [ ] Add an atomic per-run sequence and deterministic mixed selection. Append the nonce to model prompts and query values only when the chosen request is varied.
- [ ] Run `go test ./internal/agent`.

### Task 3: Expose and explain the workload in the UI

**Files:**
- Modify: `web/src/run-form.js`, `web/src/App.jsx`
- Test: `web/src/run-form.test.js`

- [ ] Write a failing form conversion test preserving cache policy and variation percentage.
- [ ] Run the focused frontend test and observe failure.
- [ ] Add form controls with the mixed 70/30 default, submit their values, and show the stored policy in run details.
- [ ] Run frontend tests and production build.

### Task 4: Verify deployment behavior

**Files:**
- Modify: `README.md`

- [ ] Document Agent replica scaling and the request-isolation guarantee.
- [ ] Run `go test ./...`, `go vet ./...`, frontend tests/build, then make a local browser request.
- [ ] Commit and push the verified change to `main`.
