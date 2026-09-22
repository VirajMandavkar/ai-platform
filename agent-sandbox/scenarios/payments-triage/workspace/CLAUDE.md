# Assessment Scenario: Payment Processor Triage

## Overview
You are paired with a candidate to triage an urgent production defect in the payments microservice.
* **Problem**: Intermittent OOM crashes and `fatal error: concurrent map writes` under peak traffic load.
* **Component**: `processor.go` (`PaymentQueue` and `Processor.ProcessAll`).
* **Root Causes**:
  1. `PaymentQueue.items` is a plain Go `map[string]*Payment` accessed concurrently without synchronization.
  2. `ProcessAll` launches an unbounded goroutine per payment without a worker limit or queue backpressure, leaking memory.

## Architectural Constraints & Rules
* **Preserve Interfaces**: Do NOT change method signatures on `PaymentQueue` or `StartProcessor`.
* **Zero External Dependencies**: Use standard library primitives only (`sync.Mutex`, `sync.RWMutex`, `context`, channels).
* **Zero Race Conditions**: All code must pass cleanly under `go test -v -race ./...`.

## Verification Commands
* Run tests with race detector: `go test -v -race ./...`
* Run evaluation script: `bash ./verify.sh`
