# Detailed Load Results Design

## Goal

Show both capacity decisions and the evidence needed to diagnose a model or web target under load.

## Result data

Each completed run stores aggregate request measurements: total, successes, failures, throughput, latency minimum/average/P50/P95/P99/maximum, and TTFT P50/P95/P99. For model requests it also stores prompt, completion, and reasoning tokens when an OpenAI-compatible response provides `usage`, plus output tokens per second. It stores counts by HTTP status and a bounded list of recent request errors. A one-second time bucket stores requests, successes, failures, and latency for the trend view.

The agent records these values for every completed response and protects shared counters with its existing measurement lock. Missing model `usage` is represented as unavailable rather than estimated. Error text is truncated and the recent-error list is capped so an unhealthy target cannot exhaust controller memory.

## APIs and storage

`RunResult` is extended with the detailed aggregates, status counts, recent errors, and time buckets. Existing create, claim, complete, get, and list-run endpoints return the richer run unchanged; no new persistence service or dependency is introduced. Current in-memory storage remains the explicit MVP ceiling: restarting the controller removes historical data.

## UI

The completed-run view presents:

1. A capacity card with stable/at-risk verdict, recommended concurrency or RPS, achieved throughput, success rate, and the configured limits.
2. Latency and TTFT percentiles, min/average/max, and model token/TPS metrics when available.
3. A compact SVG/CSS-free HTML trend table/chart showing per-second throughput, errors, and P95 latency.
4. Status-code and recent-error breakdowns.
5. A run-record table that includes target/model, load configuration, prompt preview, completion time, verdict, and a selected record's full details.

The configurable verdict defaults to error rate at most 2% and P95 latency at most 2 seconds. The UI labels a result as at risk if either limit is exceeded; otherwise it is stable. A single run recommends its configured VU/RPS only when stable. Determining a true maximum requires users to execute an increasing series of runs, so the product does not claim that one passing run is the absolute capacity limit.

## Error handling and tests

Response-body decoding remains bounded to the model response needed for `usage`; invalid or absent JSON does not turn a successful HTTP response into a failed request. Tests cover percentile and aggregate calculations, model usage extraction, status/error aggregation, API serialization, and result/record rendering. End-to-end validation uses the supplied LM Studio endpoint with a deliberately short run; it does not submit a one-million-token generation.

## Scope

This change covers OpenAI chat completions and generic web requests only. It does not add streaming-token TTFT, persistent databases, distributed workers, authentication, or automated load-step searches.
