# Load Test MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an air-gapped Kubernetes MVP for bounded HTTP load tests of registered web and OpenAI-compatible model targets.

**Architecture:** A Go controller stores targets and runs in PostgreSQL. A Go agent claims a queued run, sends concurrent HTTP requests, measures latency and TTFT, and returns aggregate results. The React UI creates runs and polls results.

**Tech Stack:** Go standard library, PostgreSQL, React, TypeScript, Vite, Podman OCI images, Kubernetes.

## Global Constraints

- Only `POST /v1/chat/completions` is supported for model tests.
- Registered hostnames and private IPv4 addresses only; public addresses are rejected.
- Server limits: 500 VUs, 2,000 RPS, 60 minutes.
- All images target `linux/amd64` and have no runtime internet dependency.
- No external SaaS, CDN, message queue, or load-testing dependency.

---

### Task 1: Domain validation

**Files:** `go.mod`, `internal/core/types.go`, `internal/core/target.go`, `internal/core/target_test.go`

**Produces:** `ValidateTarget(string) error`, `ValidateRunConfig(RunConfig) error`, and shared target/run/result structs.

- [ ] Write failing tests:

```go
func TestValidateTargetRejectsPublicHost(t *testing.T) {
    if ValidateTarget("https://example.com") == nil { t.Fatal("public host accepted") }
}
func TestValidateRunConfigRejectsTooManyVUs(t *testing.T) {
    if ValidateRunConfig(RunConfig{Mode: "vu", VUs: 501, DurationSeconds: 60}) == nil { t.Fatal("VU limit missing") }
}
```

- [ ] Run `go test ./internal/core`; expect a missing-package failure.
- [ ] Implement URL parsing, private IP / `.internal` hostname validation, and numeric limits.
- [ ] Run `go test ./internal/core`; expect PASS.
- [ ] Commit: `git add go.mod internal/core; git commit -m "feat: add load test validation"`.

### Task 2: Controller API

**Files:** `internal/store/store.go`, `internal/controller/server.go`, `internal/controller/server_test.go`, `cmd/controller/main.go`

**Consumes:** core structs. **Produces:** `POST /api/targets`, `POST /api/runs`, `GET /api/runs/{id}`, `POST /api/agent/claim`, and `POST /api/agent/runs/{id}/result`.

- [ ] Write failing test:

```go
func TestCreateRunReturnsQueuedRun(t *testing.T) {
    s := NewServer(NewMemoryStore())
    r := httptest.NewRequest(http.MethodPost, "/api/runs", strings.NewReader(`{"target_id":"t1","mode":"vu","vus":2,"duration_seconds":1}`))
    w := httptest.NewRecorder(); s.ServeHTTP(w, r)
    if w.Code != http.StatusCreated { t.Fatalf("got %d", w.Code) }
}
```

- [ ] Run `go test ./internal/controller`; expect undefined-server failure.
- [ ] Implement the memory-backed controller first, including run validation and a status transition from `queued` to `running` to `completed`.
- [ ] Run `go test ./internal/controller`; expect PASS.
- [ ] Commit: `git add cmd/controller internal/controller internal/store; git commit -m "feat: add controller run API"`.

### Task 3: HTTP load agent

**Files:** `internal/agent/runner.go`, `internal/agent/runner_test.go`, `cmd/agent/main.go`

**Consumes:** claimed run data. **Produces:** result counts, P95 latency, P95 TTFT, and error rate.

- [ ] Write failing test:

```go
func TestRunCountsSuccessfulRequests(t *testing.T) {
    target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
    result := Run(context.Background(), target.URL, core.RunConfig{Mode: "vu", VUs: 1, DurationSeconds: 1})
    if result.Successes == 0 { t.Fatal("no request succeeded") }
}
```

- [ ] Run `go test ./internal/agent`; expect undefined-runner failure.
- [ ] Implement VU workers and optional RPS pacing with `net/http`, first-byte timing via response-body read, JSON body support for model calls, and result aggregation.
- [ ] Run `go test ./internal/agent`; expect PASS.
- [ ] Commit: `git add cmd/agent internal/agent; git commit -m "feat: add HTTP load agent"`.

### Task 4: React dashboard

**Files:** `web/package.json`, `web/vite.config.ts`, `web/src/main.tsx`, `web/src/App.tsx`, `web/src/App.css`, `web/src/api.ts`

**Consumes:** Controller JSON API. **Produces:** target form, run form, and refreshing result panel.

- [ ] Write failing test that sets VUs to 10, submits `테스트 시작`, and expects `createRun` to receive `{ vus: 10 }`.
- [ ] Run `cd web; npm test -- --run`; expect absent-app failure.
- [ ] Build the smallest accessible dashboard: target URL/type inputs, VU/RPS/duration inputs, start button, run status, and success/error/P95 summary.
- [ ] Run `cd web; npm test -- --run`; expect PASS.
- [ ] Commit: `git add web; git commit -m "feat: add load test dashboard"`.

### Task 5: Air-gapped Kubernetes deployment

**Files:** `deploy/Dockerfile.controller`, `deploy/Dockerfile.agent`, `deploy/Dockerfile.web`, `deploy/k8s.yaml`, `deploy/check-manifest.ps1`, `deploy/load-images.ps1`, `README.md`

**Consumes:** three local OCI images. **Produces:** controller/agent/web Deployments, PostgreSQL StatefulSet, Services, ConfigMap, Secret, and NetworkPolicy.

- [ ] Write a failing `deploy/check-manifest.ps1` assertion for `kind: NetworkPolicy` and `kind: StatefulSet`.
- [ ] Run `powershell -File deploy/check-manifest.ps1`; expect missing-manifest failure.
- [ ] Add `linux/amd64` multi-stage Dockerfiles, one manifest, Podman `save/load` commands, and internal-image placeholders passed through `IMAGE_REGISTRY`.
- [ ] Run `powershell -File deploy/check-manifest.ps1`; expect PASS.
- [ ] Commit: `git add deploy README.md; git commit -m "feat: add air-gapped Kubernetes deployment"`.

## Plan self-review

- The five tasks cover target safety, bounded workloads, OpenAI-compatible requests, TTFT, web HTTP traffic, UI, storage boundary, Prometheus-ready deployment, Podman transfer, and Kubernetes.
- No placeholder implementation work remains; PostgreSQL is added only after the in-memory controller behavior is verified.
