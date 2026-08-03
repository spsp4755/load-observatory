# Model profiles implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Save and reuse model endpoint credentials without exposing API keys in the UI.

**Architecture:** Extend the existing `Target` persistence record with an API key and return a redacted copy to browser APIs. The assigned run receives the original target inside the Controller-Agent trust boundary. React consumes target list/create/delete endpoints.

**Tech Stack:** Go standard library, React/Vite, existing PostgreSQL JSON persistence.

## Global Constraints

- API keys must never appear in list/create browser responses or test records.
- No dependency or external secret service is introduced.

---

### Task 1: Target profile API

**Files:** `internal/core/types.go`, `internal/store/store.go`, `internal/controller/server.go`, `internal/controller/server_test.go`

- [ ] Add failing tests that target lists redact `api_key` and delete removes a target.
- [ ] Run `go test ./internal/controller` and confirm the endpoint tests fail.
- [ ] Add target list/delete operations and redacted public target responses.
- [ ] Run `go test ./internal/controller`.

### Task 2: Agent credential use

**Files:** `internal/agent/runner.go`, `internal/agent/runner_test.go`

- [ ] Add a failing test expecting `Authorization: Bearer test-key` for a model request.
- [ ] Run `go test ./internal/agent` and confirm it fails.
- [ ] Set the header only for an assigned target with an API key.
- [ ] Run `go test ./internal/agent`.

### Task 3: Profile controls

**Files:** `web/src/api.js`, `web/src/App.jsx`

- [ ] Add browser API helpers, profile selection, registration and deletion controls.
- [ ] Build and inspect the saved-profile selection in the browser.
