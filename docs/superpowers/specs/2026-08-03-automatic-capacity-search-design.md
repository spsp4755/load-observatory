# Automatic Capacity Search Design

## Goal

Automatically find the highest stable VU or RPS setting for an existing closed-network target without requiring the user to manually create every load-test step.

## Search configuration

An automatic test accepts the existing target and request settings plus load mode, start load, maximum load, duration per step, maximum error percentage, and maximum P95 latency. Defaults are 5 VU for VU mode, 10 RPS for RPS mode, 60 seconds per step, 2% errors, and 2 seconds P95 latency. Existing platform limits remain in effect.

## Execution

The controller creates an automatic-search job. The Agent executes one ordinary load run at a time. Starting at the configured value, the controller doubles the load after each stable result. A completed run is stable only when its total requests are nonzero, its error rate is within the configured threshold, and its P95 latency is within the configured threshold.

On the first at-risk step, the controller performs integer binary search between the most recent stable load and the at-risk load until the interval is one unit. It records every step and completes with the highest stable load as the recommendation. If the first step is at risk, the recommendation is zero and the result explicitly states that even the starting load was not stable. If every step through the configured maximum is stable, the recommendation is the maximum and the result states that the configured ceiling was reached.

## Control and safety

Only one step of an automatic job is queued at once. Users can cancel queued or running automatic jobs; cancellation prevents scheduling further steps while allowing the active Agent request context to finish its current run. The Controller uses the existing private-IP or `.internal` target validation and the existing single Agent queue.

## UI and records

The test page adds an automatic-search mode beside manual execution. It exposes start and maximum load fields and shows current phase, completed step count, next load, and a stop action. Records show an automatic job summary with each underlying step, its verdict, P95, success rate, and throughput. The final summary names the recommended maximum and whether the configured maximum was reached.

## Scope and verification

This MVP is sequential and in-memory. Controller restart removes jobs and their history; no parallel sweeps, scheduling, persistent database, or distributed coordination are added. Tests cover stable doubling, at-risk binary search, first-step failure, maximum-ceiling completion, cancellation, API serialization, and UI configuration conversion. Browser verification uses a short LM Studio run with a conservative maximum.
