# Scoped Agent Cancellation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop only HTTP requests issued by the cancelled Load Observatory run.

**Architecture:** Controller marks the automatic search's own runs cancelled. The claiming Agent polls that specific run and cancels its local context when cancelled.

**Tech Stack:** Go standard library.

### Task 1: Cancellation state

- [ ] Test cancellation marks running and queued search runs cancelled.
- [ ] Keep cancelled runs cancelled when result reporting arrives.
- [ ] Test store/controller behavior.

### Task 2: Agent polling

- [ ] Test a cancelled claimed run cancels the local RunTarget context.
- [ ] Poll only `/api/runs/{claimed-id}` while executing that ID.
- [ ] Run full tests and push.
