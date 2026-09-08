# Go versus Rust decision gate

## Decision objective

Choose the implementation language before production code is written. Both candidates can satisfy the
protocol. The decision should be based on a small disposable spike and operational fit, not preference
alone.

## Required spike

Build the same throwaway vertical slice in Go and Rust:

- bind an authenticated ephemeral loopback HTTP endpoint;
- launch a fake newline-delimited JSON-RPC child in a new process group;
- issue two concurrent requests and correlate reversed responses;
- forward one notification to an SSE response;
- cancel a request and prove the child/process group exits;
- create an owner-only Unix socket and exchange one authenticated control frame;
- produce a universal or architecture-specific macOS release artifact through CI.

Spike code is discarded after measurements; it is not the seed for production unless its dependencies
and design pass review.

## Evaluation matrix

Score each category from 1 to 5 and record evidence.

| Category | Weight | Evidence |
| --- | ---: | --- |
| process groups, signals, cancellation | 20 | leak tests and code complexity |
| async HTTP/SSE and backpressure | 15 | correctness under disconnect/load |
| JSON-RPC and typed JSON ergonomics | 10 | invalid-shape handling and boilerplate |
| Unix socket permissions and packaging | 10 | filesystem audit and artifact install |
| memory footprint and cold start | 10 | median/p95 across repeated runs |
| race-safety tooling | 10 | race detector, sanitizer, or model checking |
| dependency/license surface | 10 | direct/transitive count and license report |
| team maintenance familiarity | 10 | named maintainers and review capability |
| binary size and cross-build CI | 5 | signed/notarizable artifact size and duration |

## Expected tradeoff

Go is likely to minimize development time, cross-compilation friction, and async/process boilerplate.
Rust is likely to provide stronger compile-time state and ownership guarantees for session/tool
lifecycles, with higher implementation complexity and compile time. Neither expectation is a decision;
the spike data and team ownership decide.

## Decision record

Record the selected language, date, scores, benchmark environment, chosen libraries and licenses,
rejected alternative, and conditions that would reopen the choice. The first production commit must
reference that record.
