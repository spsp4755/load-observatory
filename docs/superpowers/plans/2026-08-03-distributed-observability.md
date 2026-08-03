# Distributed observability implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run one test across Agent shards, collect system metrics, and make capacity decisions from model-specific thresholds.

**Architecture:** Parent Runs create shard assignments in the existing store. A Controller monitor queries Prometheus and stores samples on the parent Run. Search consumes the merged result.

**Tech Stack:** Go standard library, React/Vite, Kubernetes, Prometheus, DCGM exporter.

## Global Constraints

- A shard cancellation must only stop its own Observatory request.
- Missing monitoring must not block a run.
- No new Go dependency.

---

### Task 1: Shard assignments

**Files:** `internal/core/types.go`, `internal/store/store.go`, `internal/store/store_test.go`, `internal/controller/server.go`, `internal/agent/client.go`

- [ ] Add a failing store test that a two-shard run issues two assignments and completes only after both results.
- [ ] Add shard state, evenly divided load, claim/report paths, and merged metrics.
- [ ] Run `go test ./...`.

### Task 2: Capacity policy

**Files:** `internal/core/search.go`, `internal/core/search_test.go`

- [ ] Add failing tests for TTFT and output-rate threshold stop messages.
- [ ] Add optional thresholds and evaluate them after error/P95.
- [ ] Run `go test ./internal/core`.

### Task 3: Monitoring deployment

**Files:** `internal/monitor/monitor.go`, `deploy/k8s.yaml`, `README.md`

- [ ] Add Prometheus query client tests for unavailable and successful responses.
- [ ] Store samples without failing a run, and add Prometheus/DCGM manifests.
- [ ] Run Go tests and deployment manifest validation.

### Task 4: UI

**Files:** `web/src/App.jsx`, `web/src/run-form.js`

- [ ] Add shard and threshold controls plus monitoring and stop-reason details.
- [ ] Run frontend tests, production build, and browser verification.
