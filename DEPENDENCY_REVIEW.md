# Phase 0 dependency, license, and provenance review

Review date: 2026-09-08. Status: candidate assessment, not distribution clearance.

## Phase 4 adoption review

The following exact archives were downloaded from the official Go module proxy with checksum-database
verification on 2026-09-08. Their complete installed LICENSE files were read before importing them;
the Go additional PATENTS grant was also read. Filename-level license/NOTICE inventory found the
top-level licenses and no separate NOTICE file in these three module archives. They are approved for
local implementation/testing subject to the stated use boundaries; this does not close release or
ownership gates. The current schemas and tests remain independently authored, with no upstream test
corpus copied into this repository.

| Module | Version | License | Module archive checksum | Intended use |
| --- | --- | --- | --- | --- |
| `github.com/santhosh-tekuri/jsonschema/v6` | v6.0.3 | Apache-2.0 | `h1:1EYB5IzjZawrrnELUi78f9fPu57HuXjmddZPjrls/28=` | Draft 2020-12 compiler/validator; untrusted work must have process-enforced deadlines and an in-memory-only resource loader |
| `golang.org/x/text` | v0.41.0 | BSD-3-Clause | `h1:vz/seA0lnX87Othu2f/0L24RcgrXD9/YFTSuGjj3rH8=` | Runtime dependency of schema validation; selected over its old v0.14.0 manifest minimum |
| `github.com/dlclark/regexp2` | v1.11.0 | MIT | `h1:G/nrcoOa7ZXlpoa/91N3X7mM3r8eIlMBBJZvsz/mxKI=` | Reviewed upstream test-graph dependency only; not approved as a runtime regex engine by this adoption |

Primary license anchors: [validator v6.0.3](https://raw.githubusercontent.com/santhosh-tekuri/jsonschema/v6.0.3/LICENSE),
[x/text v0.41.0](https://raw.githubusercontent.com/golang/text/v0.41.0/LICENSE),
[regexp2 v1.11.0](https://raw.githubusercontent.com/dlclark/regexp2/v1.11.0/LICENSE).
The x/text v0.41.0 module manifest additionally names x/tools v0.48.0, x/mod v0.38.0 and x/sync v0.22.0
for its broader build/development graph. They are not assumed to be linked or licensed merely from
that manifest. Record the compiled and test graphs after imports, retain applicable notices with the
artifact, and run an advisory review before release. No vulnerability-free or complete supply-chain
clearance is claimed from version freshness alone.

After imports and `go mod tidy`, the application/test compiled package graph contains exactly two
external modules: jsonschema v6.0.3 and x/text v0.41.0. `go list -m all` additionally resolves
regexp2 v1.11.0, x/mod v0.38.0, x/sync v0.22.0 and x/tools v0.48.0 through dependency manifests.
Those additional modules are not in `go list -test -deps ./...`; this project does not run upstream
dependency test suites. The new helper and MCP child are built from the same application module.
This graph evidence is distinct from the final binary, vendored Unicode/runtime components, notices,
advisory and release-artifact audits, which remain outstanding.

## Historical Phase 1–3 implementation scope

The user selected Go. Through Phase 3 the implementation and independent Go fake process
used no external modules. Go 1.27.1 darwin/arm64 was downloaded through the official Go
toolchain mechanism into the ignored repository cache; its installed LICENSE (BSD-3-Clause) and
PATENTS grant were read. `go.mod` records Go 1.27.0 with toolchain 1.27.1. The initial host's Go 1.22.0
was used only to bootstrap the selected toolchain. Runtime/vendor/race-test component inventory is
still a release task. The following Phase 0 candidate assessment is historical. No Rust dependency
has been adopted; the Phase 4 update above supersedes the earlier schema-candidate status.

No project license has been selected. At the Phase 0 review no dependency was installed or imported,
no module/lock file was created, and no full build/test dependency graph existed. Library names below are recommendations
for the proposed experiments or later phases. A top-level license or manifest declaration does not
establish the licenses of all transitive, generated, vendored, target-specific, or test components.

## Repository owner's rights checklist

The checklist comes from [CLEAN_ROOM_BOUNDARY.md](CLEAN_ROOM_BOUNDARY.md). It was raised during this
review; the user supplied language-familiarity information, not rights/distribution attestations.
Do not convert missing answers into affirmative records.

| Required fact | Status | Evidence needed to close |
| --- | --- | --- |
| Ownership of the original contribution, including employment duties | Unconfirmed | Owner's recorded ownership/assignment facts; Git authorship alone is insufficient |
| Co-authored or upstream expression in the behavioral specification | Unconfirmed | Specification author's provenance statement; resolve uncertain expression without showing the earlier source to this implementation agent |
| Employer invention/assignment/open-source policies | Unconfirmed | Owner's applicable policy/assignment determination, including an explicit not-applicable statement if true |
| Permission to use Kiro CLI and private extensions in the intended environment | Unconfirmed | Applicable account/service/employer terms and permitted use; technical accessibility does not establish permission |
| Licenses of every implementation and test dependency | Partial | Candidate review below, then exact resolved graph, component license texts, notices, and intended-use review |
| Internal versus external distribution | Unconfirmed | Owner's distribution plan, including whether Kiro/client/toolchains will be bundled |

Independent implementation is the provenance method, not a legal finding of non-derivation. Copyright
guidance distinguishes underlying methods from their original expression; it does not settle this
repository's ownership or employment facts. The existing boundary requires legal review if those
rights are unclear. See [U.S. Copyright Office Circular 33](https://www.copyright.gov/circs/circ33.pdf).
Leave the software-license decision open until the checklist supports it.

At review start, the nine specification/instruction Markdown files were untracked. The required
specification-only baseline commit remains to be recorded before implementation. This review made no
commit and inspected no earlier implementation, remote, source, commit, fixture, or test.

## Recommended Go dependencies

| Component | Proposed use | License evidence | Remaining conditions |
| --- | --- | --- | --- |
| Go toolchain/runtime and standard library | HTTP/SSE, JSON, process lifecycle, Unix sockets, CSPRNG, SHA-256/HMAC, logging, flags, testing/fuzzing | BSD-3-Clause top-level [Go LICENSE](https://go.dev/LICENSE) | Pin a supported toolchain; inventory bundled exceptions and the race-test runtime separately from the shipped binary |
| `github.com/santhosh-tekuri/jsonschema/v6` | Compile and validate tool argument schemas, starting in Phase 4; candidate for a small Phase 0 schema probe | Apache-2.0, reviewed at [v6.0.3 LICENSE](https://raw.githubusercontent.com/santhosh-tekuri/jsonschema/v6.0.3/LICENSE); [package documentation](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6@v6.0.3) | Candidate version v6.0.3, not yet selected in a lock file. Test Draft 2020-12 behavior, loader policy, regex compatibility, numerical precision and resource bounds. Retain required attribution/NOTICE if present. |
| `golang.org/x/text` | Schema dependency declared by the candidate | BSD-3-Clause at the declared minimum [v0.14.0 LICENSE](https://raw.githubusercontent.com/golang/text/v0.14.0/LICENSE) | The candidate's manifest minimum is not a security-approved final version; inspect actual resolution, notices, Unicode data and advisories |
| `github.com/dlclark/regexp2` | Declared upstream test dependency; possible future regex adapter only if justified | MIT at [v1.11.0 LICENSE](https://raw.githubusercontent.com/dlclark/regexp2/v1.11.0/LICENSE) | Do not assume it is linked into the product. Review any bundled attribution and computational limits before deliberately using it at runtime. |
| `golang.org/x/sys/unix` | Optional platform adapter if standard syscall coverage is inadequate | BSD-3-Clause [upstream LICENSE](https://raw.githubusercontent.com/golang/sys/master/LICENSE) | No version selected. The Phase 1/spike plan can first use the standard library's macOS process/signal facilities. |

The schema candidate's [v6.0.3 module manifest](https://raw.githubusercontent.com/santhosh-tekuri/jsonschema/v6.0.3/go.mod)
declares `x/text v0.14.0` and `regexp2 v1.11.0`, identifying the latter as test use. This is manifest
evidence, not a resolved module or binary inventory. Count the module graph, compiled package graph,
test graph, and shipped components separately. A module's `require` line alone does not prove that
its code is present in the final executable.

Use typed local ACP/Anthropic envelopes and small protocol adapters rather than selecting a broad SDK
before its scope is needed. The gateway speaks to a local CLI, so it does not need a provider SDK,
remote authentication client, general MCP management library, or HTTP framework in Phase 1. This is a
scope/dependency choice, not permission to improvise protocol semantics or hand-write a JSON Schema
validator.

The candidate validator permits a custom resource loader and normally uses Go's regexp engine.
Configure an in-memory-only loader; never resolve an untrusted schema through HTTP or the filesystem.
Choose and test a regex policy explicitly, because Draft 2020-12 support does not imply that every
ECMAScript regex expression is accepted by the default engine. A timeout around a synchronous
validation call does not reclaim CPU already executing inside it. Bound schema/instance size, depth,
references, compiled-cache bytes and validation concurrency, and measure expensive inputs.
[Validator configuration](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6@v6.0.3),
[JSON Schema core](https://json-schema.org/draft/2020-12/json-schema-core),
[JSON Schema validation](https://json-schema.org/draft/2020-12/json-schema-validation).

## Rust comparison candidates

These packages establish a plausible alternative stack. Most evidence below is an upstream license
file or declared SPDX expression rather than a reviewed release archive. Pin exact versions and
features and finish the component review before running either experiment with them.

| Component | Proposed use | Observed license/source | Scope controls |
| --- | --- | --- | --- |
| Rust toolchain/standard library | Core types, Unix process setup and build/test tools | Generally MIT OR Apache-2.0; project/component exceptions apply. [Official policy](https://rust-lang.org/policies/licenses/) | Pin stable toolchain, targets and SDK; review shipped runtime and toolchain components separately |
| `tokio` | Async process, socket, timers, I/O, bounded channels | MIT [LICENSE](https://raw.githubusercontent.com/tokio-rs/tokio/master/LICENSE) | Enable only required runtime/net/process/io/sync/time/signal features; do not assume `Child` drop performs cleanup |
| `axum` | HTTP routing and SSE encoding | MIT [LICENSE](https://raw.githubusercontent.com/tokio-rs/axum/main/LICENSE) | Keep HTTP/1 and required Tokio/JSON support; avoid unrelated websocket/form features |
| `serde`, `serde_json` | Typed envelopes and controlled unknown fields | MIT OR Apache-2.0 declared in [serde manifest](https://raw.githubusercontent.com/serde-rs/serde/master/serde/Cargo.toml) and [serde_json manifest](https://raw.githubusercontent.com/serde-rs/json/master/Cargo.toml) | Review selected releases and derive/build dependencies; preserve needed JSON numeric precision explicitly |
| `tokio-stream` | Bounded receiver to SSE stream | MIT declared in [manifest](https://raw.githubusercontent.com/tokio-rs/tokio/master/tokio-stream/Cargo.toml) | Minimal receiver adapter features; count resulting transitive dependencies |
| `tokio-util` | Optional cancellation/codec utilities | MIT declared in [manifest](https://raw.githubusercontent.com/tokio-rs/tokio/master/tokio-util/Cargo.toml) | Add only if useful; do not equate a cancellation token with completed process cleanup |
| `nix` | Safe POSIX process-group signal interface | MIT at [v0.30.1 LICENSE](https://raw.githubusercontent.com/nix-rust/nix/v0.30.1/LICENSE) and [manifest](https://raw.githubusercontent.com/nix-rust/nix/v0.30.1/Cargo.toml) | v0.30.1 is the reviewed license anchor, not an automatic choice of best version; review selected release and process/signal features |
| `getrandom` | OS cryptographic entropy | MIT OR Apache-2.0 declared in [manifest](https://raw.githubusercontent.com/rust-random/getrandom/master/Cargo.toml) | Use target-appropriate OS randomness; include target dependencies in review |
| `sha2` | Later compatibility/history/model digests | MIT OR Apache-2.0 declared in [manifest](https://raw.githubusercontent.com/RustCrypto/hashes/master/sha2/Cargo.toml) | Not required for the transport slice; review digest and target acceleration dependencies before adoption |
| `jsonschema` | Draft 2020-12 tool argument validation | MIT [LICENSE](https://raw.githubusercontent.com/Stranger6667/jsonschema/master/LICENSE); [documentation](https://docs.rs/jsonschema/latest/jsonschema/) | Disable default HTTP/file resolving and use an in-memory retriever. Review feature-unified graph, regex limits and exact-number behavior. |

The Rust validator documents default HTTP and local-file reference resolution. Its documented
`default-features = false` disables bundled resolving, but the selected graph and retriever must still
be verified; another dependency can enable features. Test that supplied schemas cause no network or
file reads. Do not include a TLS/client stack merely for `$ref` retrieval.
[Rust validator external-reference behavior](https://docs.rs/jsonschema/latest/jsonschema/#external-references).

Expected transitive components such as the HTTP stack, Tokio I/O support, proc macros, POSIX bindings,
and schema/regex/numeric dependencies must be inventoried after resolution. Their exact count and
licenses are not asserted here. Rust is viable with these candidate top-level licenses; its larger
proposed graph means more review work, not demonstrated license incompatibility.

## Test, build, and external executable scope

Both spikes may use one new Python standard-library-only fake ACP process and black-box driver.
Python is test-only, never a shipped proxy requirement. Python's license history includes the PSF
agreement and component/historical notices; review the exact interpreter distribution rather than
reducing its complete contents to a single label.
[Python license documentation](https://docs.python.org/3/license.html).

Go's built-in test/fuzz/race tooling and Rust's built-in tests suffice for the initial functional
experiment. If a Rust model checker, a property-test package, a license scanner, a CI action, or a
third-party JSON Schema test suite is later introduced, review its license and full test/build graph
before adoption. No such additional package is approved by this document.

Kiro CLI and the Anthropic client are separately installed black-box executables in the current
product scope. Their redistribution, account/service terms and private-extension permissions are
separate from Go/Rust library licenses. Do not bundle their binaries or credentials on the basis of
this dependency review. Apple SDK/toolchain and signing/notarization terms also require the appropriate
build/release environment; no CI run or signed artifact is claimed here.

## Obligations and unresolved risks

| License/risk class | Required handling in the intended distribution |
| --- | --- |
| BSD-3-Clause | Preserve copyright, license conditions and disclaimer in appropriate source/binary distribution materials; respect the non-endorsement condition |
| MIT | Preserve the applicable copyright and permission notice |
| Apache-2.0 | Include the license, retain applicable notices/attributions, preserve relevant upstream NOTICE material if supplied, and mark modified upstream files; consider its patent grant/termination terms |
| MIT OR Apache-2.0 | Record the chosen allowed license branch and preserve its obligations; an OR declaration is not an obligation to license the proxy under both |
| Unapproved copyleft or unknown/nonstandard terms | Leave the component unapproved until reviewed for its exact use and distribution; a development tool, linked runtime, fixture corpus, and bundled executable have different roles |
| Provenance/ownership | An independent rewrite and permissive dependencies do not establish employer rights or remove copied-expression risk; close the owner checklist |
| Runtime supply chain | Dependency access, schema retrieval, generated code, build scripts, optional features and target-specific code may enlarge the reviewed surface |

License summaries derive from the linked component license texts and
[Apache's license terms](https://www.apache.org/licenses/LICENSE-2.0). They are a compliance worklist,
not a legal determination that a particular distribution is cleared. No incompatible copyleft has
been identified in the reviewed top-level candidate declarations, but no complete graph has been
resolved; it would be incorrect to report a copyleft-free release or a complete audit.

## Required evidence before adoption and release

1. For the experiment, pin compiler/interpreter versions and exact direct versions/features, resolve
   the graph, and record component license evidence before using dependencies. For production, retain
   `go.mod`/`go.sum` or `Cargo.toml`/`Cargo.lock`; lock files alone are not license reports.
2. Inventory direct/transitive and build/test components separately from linked/bundled components.
   Go evidence includes module metadata, compiled dependency packages and binary build metadata. Rust
   evidence includes locked Cargo metadata and target/feature-specific normal/build/dev graphs.
3. For every selected component record name, exact version/checksum, origin, purpose, scope/target,
   SPDX expression, actual license/NOTICE paths, chosen license branch if applicable, reviewer/date,
   and approval or unresolved issue. Review generated/vendor subcomponents and security advisories.
4. Retain approved notice texts with the artifact and inspect the release contents. Avoid copying
   dependency examples or fixtures merely because the library itself is approved.
5. Repeat the graph/notice review when versions/features change and for release artifacts. Complete
   the owner's rights/distribution checklist before choosing a permissive or proprietary project
   license. Satisfy acceptance I's no-unapproved-copyleft gate for the intended distribution.

No LICENSE file, copyright ownership claim, or statement that the earlier project's licensing risk
has been eliminated is introduced by this review.

## Phase 6 image-header dependency review

Reviewed 2026-09-08 before adoption: `golang.org/x/image` v0.45.0 (Go 1.25 minimum) is proposed only
for the WebP header decoder, alongside Go's standard PNG/JPEG/GIF header decoders. The pinned official
[image LICENSE](https://raw.githubusercontent.com/golang/image/v0.45.0/LICENSE) is BSD-3-Clause and was
read in full. Header decoding avoids allocating full pixel buffers in the gateway. No upstream image,
example or test fixture will be incorporated.

Its module manifest requires the already reviewed `golang.org/x/text` v0.41.0 and `golang.org/x/sys`
v0.47.0. The pinned official [sys LICENSE](https://raw.githubusercontent.com/golang/sys/v0.47.0/LICENSE)
was read in full and is also BSD-3-Clause. These pins are approved for development under the recorded
notice obligations. Archive checksums, internal component notices, actual compiled package usage and
advisories still need recording before the media adapter/release is declared verified. Merely being
in a module manifest does not imply that x/sys is linked into the WebP adapter.

Before imports, the installed archives' complete LICENSE and PATENTS files were also read. The
filename inventory found those top-level files and no additional LICENSE/NOTICE/COPYING entry.
Checksum-database verified archives: x/image v0.45.0
`h1:FMb1nTbH5H9vF55SriQHgFw5GnNL9Jg6L25BwXKzhB0=` and x/sys v0.47.0
`h1:o7XGOvZQCADBQQ4Y7VNq2dRWQR7JmOUW8Kxx4ZsNgWs=`. Their origins are the official
`go.googlesource.com/image` and `/sys` tags. Source examples and test fixtures were not read or copied.

After the WebP import and `go mod tidy`, `go list -test -deps ./...` reports three external compiled
modules: jsonschema v6.0.3, x/image v0.45.0 and x/text v0.41.0. x/sys remains only in the resolved
module graph; it is not in the compiled application/test packages. The retained licenses/notices and
security advisory review for shipped components are still separate release work.
