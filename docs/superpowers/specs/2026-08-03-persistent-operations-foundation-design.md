# Persistent Operations Foundation Design

## Goal

Provide durable, owner-scoped, budgeted load testing for closed-network Kubernetes deployment using a PostgreSQL instance shipped with Load Observatory.

## Deployment

The K8s manifest adds a PostgreSQL StatefulSet, ClusterIP Service, Secret for credentials, and a 10Gi PVC using a configurable storage class. Controller receives `DATABASE_URL`; PostgreSQL is internal to the `load-observatory` namespace. Podman image loading remains unchanged, with an additional PostgreSQL image documented for offline import.

## Persistence

Controller uses a store interface implemented by PostgreSQL. Tables persist targets, runs, automatic searches, search-step mapping, and audit events. Startup creates schema with idempotent SQL. Existing memory data is not migrated; new deployment starts with an empty durable database. Controller restart preserves completed and running work metadata; agents may reclaim only queued work.

## Ownership and audit

The web client creates a random browser worker ID once and sends it as `X-Load-Observatory-Owner`. Controller requires this header for user-facing target/run/search APIs, records it with each item, and filters reads and cancellation by it. Agent endpoints use an internal agent token header rather than owner identity. Audit events record create, claim, completion, cancellation, and budget rejection with owner, resource ID, and timestamp.

## Budgets

Controller rejects a new run/search when the owner already has one active search/run, VU/RPS/duration/token values exceed existing global limits, or configured run token budget exceeds 1,000,000. Budget validation happens before targets or runs are queued. Existing Agent cancellation remains scoped to the specific owned search run.

## Scope

This phase does not add external identity providers, Prometheus, distributed agent scheduling, scheduling, or report generation. They will use the persisted ownership and audit model in later phases.

## Verification

Tests cover owner filtering, unauthorized cancellation, budget rejection, SQL persistence across a new store instance, and schema creation. K8s checks verify PostgreSQL resources and required secrets. Browser tests verify worker ID header use and that records persist after a Controller restart against the local PostgreSQL service when available.
