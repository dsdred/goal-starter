# TASK: Add SSE Handler Tests for Logs API

**ID:** TASK-LOGS-SSE-TESTS
**Priority:** P1
**Size:** Bounded (handler unit tests only)

## Goal

Add direct unit tests for the SSE log streaming handlers to verify disconnect, cancel, and heartbeat semantics.

## Problem

The Logs SSE implementation (`serveLogStream`) currently has no direct handler-level tests in `internal/webui/handlers/`. The semantics rely on LogBroker tests and manual smoke checks. We need tests for:

- client disconnect (`r.Context().Done()`) → subscription cancelled;
- subscription cancel → handler returns;
- heartbeat ticker stops (no goroutine leak);
- no sends after subscription done (LogBroker contract);
- per-instance and global stream paths.

## Current behavior

`serveLogStream` exists and works in manual smoke tests, but lacks direct test coverage.

## Desired behavior

- `TestLogsStream_DisconnectCancelsSubscription`
- `TestLogsStream_HeartbeatStopsOnReturn`
- `TestInstanceLogStream_ContextCancelStopsStream`

## In scope

- Only test files under `internal/webui/handlers/`.
- No production code changes.

## Out of scope

- LogBroker internal tests (already exist).
- Performance/load testing.

## Acceptance criteria

- Each test passes with `go test -race ./internal/webui/handlers/`.
- No goroutine leaks after tests (verified by test timeout + runtime checks).
- Coverage includes the full SSE loop: subscribe → stream → cancel/disconnect.
