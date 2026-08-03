# Distributed execution and observability design

A parent Run owns a configurable number of shard assignments. Each Agent claims one pending shard, receives an evenly divided VU/RPS configuration, and reports its result to the parent. The Controller aggregates all shard results only when every shard completes, preserving one public run and one automatic-search decision.

The Controller samples Prometheus during a run and records available GPU, CPU, and memory metrics. Kubernetes manifests deploy Prometheus and DCGM exporter; unavailable endpoints yield an explicit unavailable monitoring record rather than failing load generation.

Capacity stability requires configured error rate and P95 plus optional TTFT P95 and minimum output tokens/second thresholds. The first violated threshold is retained as the stop reason.
