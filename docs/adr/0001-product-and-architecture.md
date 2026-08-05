# ADR 0001: GoAl product and architecture

## Status

Accepted for starter repository.

## Context

GoAl manages local AI runtimes such as llama.cpp variants and other executable inference servers. The service must use little memory and disk, run on a configurable local-network port, and ship without Python, Node.js, or a JVM.

## Decision

- Use Go.
- Ship one executable with embedded web assets.
- Treat Runtime, Model, and Profile as separate domain entities.
- Keep process lifecycle behind a Process Manager.
- Keep OS-specific process handling behind `internal/platform`.
- Support Windows and Linux as first-class targets.
- Use JSON storage for the MVP.
- Use SSE for one-way live logs.
- Do not use a shell to launch managed processes.

## Process ownership

A managed process has exactly one goroutine that calls `cmd.Wait()`. Stop requests signal the platform layer and wait on the same completion result rather than calling `Wait()` again.

## Windows strategy

Use a Windows Job Object to own the full process tree. Attempt a console control event for graceful shutdown where compatible, then terminate the Job Object after timeout.

## Linux strategy

Create a dedicated process group. Send SIGTERM to the group and escalate to SIGKILL after timeout.

## Consequences

Most application code remains platform-independent. Windows behavior must be integration-tested on Windows because cross-compilation alone cannot validate Job Object and console-event semantics.
