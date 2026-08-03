# Run Snapshot and Verdict Message Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Explain automatic-search failure with measurements and preserve each run's submitted settings.

**Architecture:** Derive the stop explanation when the search completes and store it in `AutoSearch.Message`; render run configuration only from the selected run. Update static model presets.

**Tech Stack:** Go standard library, React, Node test runner.

## Global Constraints

- No additional services or dependencies.
- Presets only change prompt/max tokens.

### Task 1: Search explanation

- [ ] Add failing core tests for zero-request, error-rate, and P95 messages.
- [ ] Store the selected failure explanation in `AdvanceSearch`.
- [ ] Run `go test ./internal/core ./internal/controller`.

### Task 2: UI snapshot and presets

- [ ] Add failing JS tests for three 5x preset values and a run snapshot detail.
- [ ] Render search messages and selected run configuration from persisted run data.
- [ ] Run `npm.cmd test --prefix web; node web/node_modules/vite/bin/vite.js build`.

### Task 3: Release

- [ ] Run full Go/frontend/manifest verification, browser check, commit, and push.
