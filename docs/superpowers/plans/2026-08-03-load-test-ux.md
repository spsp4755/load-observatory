# Load Test UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make test setup and historical records understandable, filterable, and comparable.

**Architecture:** Add pure client helpers for presets, record decoration, filtering, sorting, and comparisons. The current APIs remain source-of-truth; controller adds only an automatic-search reference on generated runs.

**Tech Stack:** Go standard library, React 19, Vite, Node built-in test runner.

## Global Constraints

- No dependencies, database, or server-side filtering.
- Keep model presets editable and limited to prompt/max tokens.
- Compare at most two runs in the browser.

---

### Task 1: Add run origin metadata

**Files:** `internal/core/types.go`, `internal/store/store.go`, `internal/controller/server_test.go`

- [ ] Write a failing assertion that an automatic claimed run contains `search_id`.
- [ ] Implement `Run.SearchID` and set it when queuing a search step.
- [ ] Run `go test ./internal/controller ./internal/store` and commit.

### Task 2: Add test/record UI helpers

**Files:** Create `web/src/record-utils.js`; Test `web/test/record-utils.test.js`

- [ ] Write failing tests for coding preset values, stable filtering, P95 sorting, and two-run delta.
- [ ] Implement `presets`, `filterRuns`, `sortRuns`, and `compareRuns` as pure functions.
- [ ] Run `npm.cmd test --prefix web` and commit.

### Task 3: Rebuild test and records UX

**Files:** `web/src/App.jsx`, `web/src/App.css`, `web/src/api.js`

- [ ] Add Manual/Automatic mode tabs, editable model presets, and concise inline guidance.
- [ ] Add record summary cards, filters, sort controls, selectable rows, comparison panel, and empty states.
- [ ] Run frontend tests and production build.

### Task 4: Verify and release

- [ ] Run Go tests, vet, frontend tests/build, and manifest check.
- [ ] Browser-test presets, filters, comparison, and existing LM Studio records.
- [ ] Commit and push to `main`.
