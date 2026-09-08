# Language decision record: Go recommended

Date: 2026-09-08. Status: **proposed; recommendation only, not the final language gate**.
Decision framework: [LANGUAGE_DECISION.md](LANGUAGE_DECISION.md).
Specification findings: [PHASE_0_REVIEW.md](PHASE_0_REVIEW.md).
Candidate libraries and license evidence: [DEPENDENCY_REVIEW.md](DEPENDENCY_REVIEW.md).

## Recommendation and rationale

Recommend Go for the standalone proxy. The user reports familiarity with Python and plans to develop
with AI assistance; they ask that performance, reliability, and feature fit determine this choice.
There is no evidence of an experienced Go or Rust maintainer/reviewer, so neither language receives a
team-familiarity advantage. AI-generated code receives the same protocol, race, and lifecycle checks
as any other code.

This workload primarily coordinates bounded HTTP streams, JSON messages, Unix sockets, and external
processes. It does not perform model inference. Go's standard library and bundled tools cover HTTP serving, explicit
SSE flushing, JSON, cancellation contexts, subprocesses, Unix sockets, cryptographic randomness and
digests, test servers, fuzzing, and race detection. Phase 1 can use no third-party runtime packages;
Draft 2020-12 validation can be added separately for Phase 4. This is a concrete reduction in APIs,
dependency review, and deployment components for this particular scope, not a measured speed claim.
See [net/http](https://pkg.go.dev/net/http), [os/exec](https://pkg.go.dev/os/exec), and the
[Go standard library](https://pkg.go.dev/std).

Go still needs deliberate ownership: one owner for pending RPC completion, serialized writes,
per-session state transitions, byte-bounded queues, and a cleanup supervisor that outlives request
cancellation. `exec.CommandContext` kills its direct process by default; it does not implement the
specified staged process-group shutdown. `WaitDelay` helps bound subprocess/pipe waits but does not
prove descendant cleanup. Those details must be tested rather than inferred from using Go.
[Go subprocess cancellation contract](https://pkg.go.dev/os/exec#Cmd).

Rust is a credible alternative. Its ownership system and typed enums help prevent invalid ownership
and state combinations, and safe concurrency catches classes of data races before execution.
[Rust's concurrency model](https://doc.rust-lang.org/book/ch16-00-concurrency.html). Tokio
and a small HTTP stack cover the required functionality. However, the proposed slice uses more
libraries, and async task cancellation still needs an explicit supervisor and OS cleanup. Tokio's
child process continues after handle drop by default; `kill_on_drop` is an extra safeguard, not the
specified graceful group shutdown. A dropped future, a bounded channel, and a safe type do not by
themselves establish exactly-once tool-result delivery. See
[Tokio process ownership](https://docs.rs/tokio/latest/tokio/process/struct.Command.html#method.kill_on_drop)
and [process groups](https://docs.rs/tokio/latest/tokio/process/struct.Command.html#method.process_group).

Rust is not preferred initially because there is no demonstrated memory/latency constraint that
outweighs Go's smaller integration surface here. This does not establish that Go is faster, uses less
memory, or is intrinsically safer. Large JSON/schema workloads, cancellation defects, or later service
constraints could change the choice. No numeric performance result is available yet.

## Evaluation matrix and decision rule

The original weights are preserved; they sum to 100. Score each category 1-5 only after the experiment.
The normalized total is `sum(weight * score) / 100`, on a 1-5 scale. A correctness/security failure is
a failed candidate regardless of its weighted total. Do not substitute expectations for measurements.

| Category | Weight | Go evidence to collect | Rust evidence to collect | Current scores |
| --- | ---: | --- | --- | --- |
| Process groups, signals, cancellation | 20 | Group leak tests, deadline coverage, ownership branches | Same tests, task/drop/reap behavior, custom unsafe code if any | Not measured |
| HTTP/SSE and backpressure | 15 | Flush latency, stalled readers, unrelated-stream progress, queue bytes | Identical stream and load schedule | Not measured |
| JSON-RPC and typed JSON ergonomics | 10 | Envelope/numeric handling, invalid-shape cases, adapter size | Same cases, serde type/error handling | Not measured |
| Unix permissions and packaging | 10 | Creation-time access, file-mode audit, install/uninstall | Identical permission and artifact checks | Not measured |
| Memory and cold start | 10 | Proxy RSS/peak RSS and launch-to-ready median/p95 | Identical measurements with shared fake-child cost separated | Not measured |
| Race-safety tooling | 10 | `go test -race`, realistic repeated interleavings | Compiler checks plus executable concurrency tests; evaluate a separately reviewed model-checking tool if needed | Not measured |
| Dependency/license surface | 10 | Direct/resolved/build/test/linked component reports | Same report with Cargo features and targets | Candidate review only; not scored |
| Team maintenance familiarity | 10 | Python familiarity only; name an accountable reviewer | Same evidence | No demonstrated advantage; if still unknown at selection, disclose neutral 3/3 as an assumption |
| Binary size and cross-build CI | 5 | Per-architecture artifact size, clean/warm build time, signing inspection | Same output and CI constraints | Not measured |

Scoring anchors: 1 means the requirement fails or lacks usable evidence; 3 means the agreed functional
and operational contract passes with documented limitations; 5 requires passing results plus a clear
measured or independently reviewed advantage. Use 2/4 only with an explanation between those anchors.
For speed/size metrics compare the same environment and uncertainty, not cross-machine numbers. A
score of 1 on a mandatory safety/correctness case cannot be compensated by a faster binary.

If both candidates pass and their scores differ by less than 0.25, prefer Go for the narrower
integration/dependency surface described above. This tie-break threshold is a proposed decision rule,
not a measured result. If a constraint or experiment changes, record it for both candidates before
using the new comparison.

## Minimal disposable experiment

Build two isolated throwaway parents, one in Go and one in Rust, against the same independently
written fake ACP child and fixture schedule. A Python standard-library-only child/driver is suitable
and uses the user's existing familiarity without making Python a product dependency. Review the
chosen Python distribution first. Write the shared wire cases and assertions before either parent.
No Kiro, external model, tool effect, persistent conversation manager, or complete launcher is needed.

The parent needs only an authenticated loopback endpoint, one ACP transport, one SSE stream adapter,
one Unix control connection, and a shutdown supervisor. The fake implements initialization, two
synthetic request/reply exchanges, a public session update, a blocked prompt/cancel path, and selectable
failure modes. Test-specific methods stay explicitly fake-only and never become production ACP APIs.

| Slice step | Identical observable contract in both languages | Evidence and failure cases |
| --- | --- | --- |
| 1. HTTP | Bind `127.0.0.1:0`; separately exercise IPv6 loopback. Use independent 256-bit model/UI/control secrets. A model route requires model auth. | Valid model token succeeds; missing/wrong/UI token gets 401. Record actual listener address. Do not expose credentials in readiness output; pass test configuration through owner-only files. |
| 2. Child and handshake | Launch the fake as an absolute executable in a new process group, isolated directory and allowlisted environment. Initialize ACP version 1 first. | Record PID/PGID and readiness via a private test channel. Reject version mismatch, exit-before-ready, malformed/non-object frames and leaked environment sentinels. |
| 3. Correlation | Send two requests concurrently, reply to the second first, insert a session notification, then reply to the first. | Exact ID/result pairing, one completion per request, notification not mistaken for a response. Include duplicate/unknown IDs and simultaneous writes. |
| 4. SSE | Forward the notification's synthetic text as a valid complete Anthropic text SSE message, flushing incrementally. | Reconstructed text equals buffered output. A paused or disconnected reader cannot hold the shared stdout reader indefinitely; overflow is explicit and an unrelated session continues until any declared process retirement. |
| 5. Cancellation | Cancel before the first event and during streaming; repeat stop/cancel concurrently. Apply cancel, stdin close, graceful wait, group TERM, then group KILL. | Both direct child and same-group grandchild disappear within the bound. Include TERM-ignoring processes, a leader that exits first, a descendant holding stdout/stderr open, blocked stdin, and stderr flooding. Reap direct children; settle all pending requests. |
| 6. Unix relay control | Create a fresh 0700 directory, a socket restricted to the owner, and 0600 secret/config. Exchange one bounded authenticated request and reply with a unique call ID. | Reject wrong secret, wrong session, duplicate ID, malformed frame and excessive size. Check the directory blocks access before socket chmod; remove socket/config after normal and canceled runs. |
| 7. macOS CI artifact | Build separate arm64 and amd64 Mach-O artifacts, with a documented deployment target and checksums; run the same smoke slice on supported macOS hardware. | Record runner/toolchain/SDK versions, artifact sizes and clean/warm build durations. Inspect native dependencies and code-signing suitability. A universal binary is optional, not required by the language gate. |

Use Go's standard HTTP/JSON/process/Unix APIs. Keep platform signal calls in a small macOS/Unix
boundary. For Rust, start with Tokio, Axum, serde/serde_json, tokio-stream, nix, and getrandom; add
tokio-util only if its cancellation/codec utilities simplify a demonstrated concern. Use explicit
features rather than broad defaults. There is no need for a full ACP, Anthropic, or MCP SDK in this
slice. The library review explains the later schema-validation dependency.

The socket control framing can be a proposed length-prefixed UTF-8 JSON object with a 4 MiB payload
ceiling, authenticated inside a versioned envelope. Use the same framing in both spikes. It must be
ratified under R09 before it becomes the product contract. MCP stdio and ACP remain newline-delimited.

### Test limits and workloads

These are common experiment settings, not new production defaults:

- ACP payload ceiling 8 MiB, relay payload ceiling 4 MiB, HTTP body ceiling 16 MiB; exercise exact
  boundaries and one-byte-over cases. Include 1 KiB, 64 KiB and 1 MiB normal JSON frames.
- Bound each event queue by both 64 entries and 16 MiB; bound pending RPCs at 64, simultaneous test
  streams at 16, and retained stderr at 64 KiB with a 4 KiB per-line and 64-line ceiling. Verify count
  and byte caps independently; reject overload before creating unbounded tasks.
- Use a 1-second first-event deadline, 5-second total test-turn deadline, and explicit write/read
  deadlines. In the cleanup test reserve 1 second for cancel/stdin/graceful work, 1 second after TERM,
  and 1 second after KILL/reap, with a separate 5-second harness watchdog. All calls, including cancel
  writes, must fit the stage budget. Repeat with shorter injected deadlines for deterministic tests.
- Run each functional case once first, then 100 lifecycle/cancellation cycles per failure mode and
  1,000 deterministic seeded correlation/cancellation schedules. Each seed and failure is retained.
- For load, run 1, 4 and 16 streams for 60 seconds, using the same fake event cadence and payload
  sizes. Include one reader that pauses for 5 seconds and a producer that exceeds the queue budget.
  Report progress/failure of the other streams and retained bytes, not just throughput.
- Separately probe each schema candidate with an independently authored small Draft 2020-12 corpus:
  internal `$ref`, `$dynamicRef`, `unevaluatedProperties`, wrong types, invalid schemas, exact large
  numbers, an unsupported/expensive regex, and denied HTTP/file references. This is a dependency-fit
  probe, not a tool-relay implementation. A context timeout alone cannot stop synchronous validation.

The fake's descendants intentionally remain in the parent's process group. Passing that test proves
cleanup of the owned group, not an OS sandbox against a malicious child that deliberately escapes it.
The Kiro execution-restriction proof is a separate R06/live gate.

### Measurements and reproducibility

Record macOS build, architecture, CPU, RAM, power mode, toolchain and Python versions, CI runner label,
SDK/deployment target, compile flags, exact dependency locks/features, and fixture digest. Prefer the
same idle Apple Silicon machine for comparative measurements; run Intel artifact smoke tests on an
Intel host or explicitly label an emulated result. Buildability is not native execution evidence.

Measure launch-to-authenticated-ready and launch-to-first-SSE-byte across 30 fresh process launches
after five excluded warmups. Report median and nearest-rank p95 with sample count; call this process
cold start, not a flushed-filesystem-cache benchmark. Measure proxy RSS, proxy peak RSS, idle/load CPU,
and child/driver resource use separately. Sample each PID on macOS and audit descriptors and surviving
PIDs after cleanup. A Python fake must not be counted as Go/Rust runtime memory.

Record clean build time over three runs and warm incremental build time over five. Measure release
binaries without race/sanitizer instrumentation; run instrumented correctness suites separately.
Report binary bytes before/after stripping, dynamic dependencies, checksum, target, and CI duration.
Go cross-build settings are `GOOS=darwin`, `GOARCH=arm64` or `amd64`, with `CGO_ENABLED=0` if the selected
dependencies permit it. Rust targets are `aarch64-apple-darwin` and `x86_64-apple-darwin`, built on
macOS with the required SDK/linker. Actual signing/notarization needs the owner's credentials and is
a release task; do not report it as completed from a successful build alone.
See [Go target/build environment](https://go.dev/doc/install/source#environment) and
[Rust macOS target requirements](https://doc.rust-lang.org/rustc/platform-support/apple-darwin.html).

The race detector covers executed Go paths only; a clean result is not a proof of all interleavings.
Rust's compiler likewise does not prove deadlock freedom or tool-result ownership across logical
sessions. Both candidates must pass the same adversarial and repeated scheduling tests.
[Go race detector limitations](https://go.dev/doc/articles/race_detector).

Retain synthetic fixtures, commands, sanitized failures, measurement tables, build/lock/license
reports and design findings. Discard the throwaway parent/child implementation after measurement;
production begins afresh from reviewed contracts and fixtures. Do not turn spike structure into an
unreviewed production template.

## Conditions to finalize or reopen

Finalize only after both slices and applicable A/G cases pass, macOS artifacts are demonstrated,
dependency graphs are reviewed, scores and the benchmark environment are filled in, the rights
checklist status is resolved as required by Phase 0, and accountable review ownership is recorded.
The first production commit must reference this record only after its status becomes final.

Reopen in favor of Rust if Go cannot meet a measured resource/latency budget, repeated Go lifecycle
defects remain after a bounded redesign, the schema candidate cannot meet required semantics within
resource limits, or a Rust-based typed design shows a material tested reliability advantage with an
acceptable dependency graph. Reevaluate either choice if platform scope or team ownership changes.

Current outcome: **Go is the technical recommendation. Measured winner, final language selection,
and software license selection remain unclaimed.**
