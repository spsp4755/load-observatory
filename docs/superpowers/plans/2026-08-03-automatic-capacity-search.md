# Automatic Capacity Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically identify and report the highest stable VU or RPS load.

**Architecture:** The controller owns a sequential in-memory search job and queues one existing `Run` at a time. When a result is reported, it evaluates thresholds, schedules the next doubling or binary-search step, and exposes the job and its runs through HTTP. React configures and displays the job.

**Tech Stack:** Go standard library, React 19, Vite, Node built-in test runner.

## Global Constraints

- Use the existing private-IP or `.internal` target validation, Agent, and memory store.
- Do not add dependencies, persistence, parallel sweeps, or distributed coordination.
- Default starts are 5 VU and 10 RPS; only one automatic step is queued at a time.
- A stable result requires nonzero total requests, error rate within threshold, and P95 within threshold.

---

### Task 1: Model search state and step transition

**Files:**
- Modify: `internal/core/types.go`
- Create: `internal/core/search.go`
- Test: `internal/core/search_test.go`

**Interfaces:** Produces `AutoSearch`, `AutoSearchConfig`, `AutoSearchStatus`, and `NextSearchLoad(search AutoSearch, result RunResult) (int, bool)`.

- [ ] Write failing tests for 5→10 doubling, 5/10 binary steps, first-step failure, and configured maximum completion.
- [ ] Run `go test ./internal/core -run TestNextSearchLoad -v` and confirm failure.
- [ ] Implement integer transition: double to at most maximum while stable; after a failing step, bisect stable and failing values until their difference is one; return no next load when complete.
- [ ] Run `go test ./internal/core -v` and commit with `git add internal/core; git commit -m "feat: model automatic capacity search"`.

### Task 2: Store, controller endpoints, and Agent completion integration

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/controller/server.go`
- Test: `internal/controller/server_test.go`

**Interfaces:** Produces `POST /api/searches`, `GET /api/searches`, `GET /api/searches/{id}`, and `POST /api/searches/{id}/cancel`. The existing result endpoint advances the parent search when its step completes.

- [ ] Write failing controller tests that create a 5-VU search, claim its first run, report a stable result, and receive a 10-VU next step; add cancellation coverage.
- [ ] Run `go test ./internal/controller -run 'TestSearch|TestCancelSearch' -v` and confirm failure.
- [ ] Store search jobs and a run-to-search mapping under the existing lock; create one step at a time and use the core transition helper after each completed result.
- [ ] Run `go test ./internal/controller ./internal/store -v` and commit with `git add internal/store internal/controller; git commit -m "feat: run automatic capacity searches"`.

### Task 3: Automatic-search interface and records

**Files:**
- Modify: `web/src/api.js`
- Modify: `web/src/App.jsx`
- Modify: `web/src/App.css`
- Test: `web/test/run-form.test.js`

**Interfaces:** Produces `createSearch(config)`, `getSearch(id)`, and `cancelSearch(id)`; consumes job fields `status`, `recommended_load`, `next_load`, and `run_ids`.

- [ ] Write a failing form conversion test expecting `start_load: 5` and `max_load`.
- [ ] Run `npm.cmd test --prefix web` and confirm failure.
- [ ] Add Manual/Automatic choice, start/max fields, an automatic progress card with stop action, and a records summary that exposes each step's verdict and final recommendation.
- [ ] Run `npm.cmd test --prefix web; node web/node_modules/vite/bin/vite.js build` and commit with `git add web; git commit -m "feat: add automatic capacity search UI"`.

### Task 4: Release verification

**Files:**
- Test: existing Go and web test suites

- [ ] Run `go test ./...; go vet ./...; npm.cmd test --prefix web; node web/node_modules/vite/bin/vite.js build; powershell -ExecutionPolicy Bypass -File deploy/check-manifest.ps1`.
- [ ] Restart local Controller and Agent, create a conservative LM Studio automatic test from 5 through 10 VU with 5-second steps, and verify search progress, recommendation, record visibility, and stop availability in a browser.
- [ ] Commit remaining files and push with `git add internal web docs; git commit -m "feat: automate capacity discovery"; git push origin main`.
