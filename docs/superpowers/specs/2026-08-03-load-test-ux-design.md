# Load Test UX Design

## Goal

Make starting tests, diagnosing a single result, and comparing capacity runs fast and understandable for operators.

## Test page

The test page has Manual and Automatic tabs. Shared target/model, prompt, max-token, load-mode, duration, and verdict settings remain visible once. Manual mode exposes the selected VU/RPS. Automatic mode exposes start/max load and explains its doubling and binary-search behavior.

Model presets reduce repetitive setup: Short response sets a short question and 256 max tokens; Coding task sets a coding prompt and 4096 tokens; Long output sets a detailed task and 32768 tokens. Presets only change prompt and maximum output tokens, never target, model, load, or verdict settings. The user can edit all values after applying a preset.

## Records page

Records begin with summary cards for total runs, stable runs, at-risk runs, and the most recent completed automatic-search recommendation. Filters support free-text target/model/run search, status, test origin, and verdict; sorting supports newest, P95 latency, error rate, and throughput. All filtering/sorting occurs in the browser over the existing in-memory API response.

Each row shows run ID, type (manual or automatic step), load, status, success rate, P95, throughput, and verdict. Selecting one row opens its full existing details below the list. Users can select up to two rows for comparison; a comparison panel shows both values and the numeric difference for load, success rate, P95, throughput, and token speed when available.

## Data flow and safety

The Controller includes the parent automatic-search ID in each generated run so the UI can label automatic steps. `GET /api/searches` supplies search summaries. No database, new dependency, or server-side query system is added. Empty filter results and missing token metrics have explicit empty states.

## Verification

Tests cover preset conversion, client filtering/sorting/comparison helpers, and automatic-run labelling. Browser validation verifies switching Manual/Automatic, applying a preset without changing a model target, filtering records, selecting two records, and reading the comparison panel using the existing LM Studio test data.
