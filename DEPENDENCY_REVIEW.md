# Phase 0 dependency, license, and provenance review

Review date: 2026-09-08. Status: candidate assessment, not distribution clearance. D121 (2026-09-11)
completes the component inventory for the darwin/arm64 development artifact in the final section and
moves the owner's rights checklist outside this repository's scope; earlier sections record their
own checkpoints.

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
review; the user supplied language-familiarity information, not rights/distribution attestations. Do
not convert missing answers into affirmative records. On 2026-09-11 (D121) the owner moved the
ownership, provenance, policy, service-terms and distribution items outside this repository's scope:
the owner answers them separately, the table is retained only as the list of facts the owner will
need, and this repository asserts no status for them. The dependency-license item is complete for
the compiled application/test graph.

| Required fact | Status | Evidence needed to close |
| --- | --- | --- |
| Ownership of the original contribution, including employment duties | Owner's external item (D121; not tracked here) | Owner's recorded ownership/assignment facts; Git authorship alone is insufficient |
| Co-authored or upstream expression in the behavioral specification | Owner's external item (D121; not tracked here) | Specification author's provenance statement; resolve uncertain expression without showing the earlier source to this implementation agent |
| Employer invention/assignment/open-source policies | Owner's external item (D121; not tracked here) | Owner's applicable policy/assignment determination, including an explicit not-applicable statement if true |
| Permission to use Kiro CLI and private extensions in the intended environment | Owner's external item (D121; not tracked here) | Applicable account/service/employer terms and permitted use; technical accessibility does not establish permission |
| Licenses of every implementation and test dependency | Complete for the compiled application/test graph (D121) | Candidate review below, then exact resolved graph, component license texts, notices, and intended-use review |
| Internal versus external distribution | Owner's external item (D121; not tracked here) | Owner's distribution plan, including whether Kiro/client/toolchains will be bundled |

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

D68's opt-in plugin-source test uses the existing `/usr/bin/git`, reporting 2.39.5 (Apple Git-154),
only as an external test executable. It authors and updates a new local fixture marketplace; no
external repository, previous implementation or Git source is read. The upstream
[Git license statement](https://git-scm.com/about) identifies GPLv2 (checked 2026-09-09). This use
adds no linked module, copied Git component or bundled executable to the proxy. Apple-distribution
components/terms and any future redistribution of Git would require a separate exact review; this
record authorizes neither bundling nor a general distribution clearance.

## Obligations and unresolved risks

| License/risk class | Required handling in the intended distribution |
| --- | --- |
| BSD-3-Clause | Preserve copyright, license conditions and disclaimer in appropriate source/binary distribution materials; respect the non-endorsement condition |
| MIT | Preserve the applicable copyright and permission notice |
| Apache-2.0 | Include the license, retain applicable notices/attributions, preserve relevant upstream NOTICE material if supplied, and mark modified upstream files; consider its patent grant/termination terms |
| MIT OR Apache-2.0 | Record the chosen allowed license branch and preserve its obligations; an OR declaration is not an obligation to license the proxy under both |
| Unapproved copyleft or unknown/nonstandard terms | Leave the component unapproved until reviewed for its exact use and distribution; a development tool, linked runtime, fixture corpus, and bundled executable have different roles |
| Provenance/ownership | An independent rewrite and permissive dependencies do not establish employer rights or remove copied-expression risk; the owner's checklist is handled outside this repository (D121) |
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
5. Repeat the graph/notice review when versions/features change and for release artifacts. The
   owner's rights/distribution checklist and the project-license choice are handled outside this
   repository (D121). Satisfy acceptance I's no-unapproved-copyleft gate for the intended
   distribution.

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

## Phase 6 terminal adapter adoption

The already reviewed and checksum-verified `golang.org/x/sys` v0.47.0 is now selected as a direct
dependency for descriptor duplication and terminal ioctls in the macOS/Linux launcher adapter.
The BSD-3-Clause LICENSE and additional PATENTS review above apply; no version change, upstream
fixture, example, or terminal-emulation library is introduced. The adapter uses named platform APIs
from the [official unix package documentation](https://pkg.go.dev/golang.org/x/sys/unix@v0.47.0).
The prior statement that x/sys was graph-only describes the media checkpoint, not this adoption.
Actual compiled graphs and final artifact notices must be updated after integration.

After integration, `go list -test -deps ./...` reports four external compiled modules: jsonschema
v6.0.3, x/image v0.45.0, x/text v0.41.0 and x/sys v0.47.0. `go.mod` now lists x/sys directly and
`go.sum` retains the already reviewed archive checksum. System `script` is a separately installed
macOS test utility, invoked by its documented argument interface without copying its source or
examples; it is neither a runtime launcher dependency nor bundled with the product. Final binary,
runtime/race component, notice and advisory audits remain open.

## Dependency inventory checkpoint — 2026-09-09

This review inspected the current module metadata, installed dependency license/PATENTS files,
license and attribution markers, embedded-resource filenames, and Go package/build metadata. It
used no previous implementation, dependency implementation as a design template, or copied fixture.
No package was installed, release artifact built, or project license selected. Earlier graph counts
above describe their respective checkpoints; the following counts describe this inspection.

### Resolved and compiled scopes

| Component | Exact version | Observed scope | License evidence and review state |
| --- | --- | --- | --- |
| Go toolchain/runtime | Go 1.27.1, darwin/arm64; module language minimum 1.27.0 | Build/test toolchain and standard-library compilation | Installed BSD-3-Clause LICENSE and PATENTS reread; bundled components require the separate review below |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3 | Direct; application/test package graph and current command target | Installed Apache-2.0 LICENSE reread; development adoption remains as recorded above |
| golang.org/x/image | v0.45.0 | Direct; application/test package graph, absent from the current command target | Installed BSD-3-Clause LICENSE and PATENTS reread |
| golang.org/x/sys | v0.47.0 | Direct; application/test package graph and current command target | Installed BSD-3-Clause LICENSE and PATENTS reread |
| golang.org/x/text | v0.41.0 | Indirect; application/test package graph and current command target | Installed BSD-3-Clause LICENSE and PATENTS reread; generated-data attribution remains below |
| github.com/dlclark/regexp2 | v1.11.0 | Resolved module graph only | Installed MIT LICENSE reread; no runtime regex adoption |
| golang.org/x/mod | v0.38.0 | Resolved module graph only; archive not inspected | Exact archive/license/component review remains open |
| golang.org/x/sync | v0.22.0 | Resolved module graph only; archive not inspected | Exact public BSD-3-Clause LICENSE read; archive/checksum/component review remains open |
| golang.org/x/tools | v0.48.0 | Resolved module graph only; archive not inspected | Exact archive/license/component review remains open |

Primary license anchors remain the pinned links in the adoption sections. The additional exact
[x/sync v0.22.0 LICENSE](https://raw.githubusercontent.com/golang/sync/v0.22.0/LICENSE) was read in
full. Fetching the exact x/mod and x/tools license anchors did not succeed, so their graph presence
does not become a completed component review.

`go list -m -json all` resolved eight external modules. `go list -test -deps ./...` identified four
external compiled modules: jsonschema, x/image, x/sys and x/text. The image packages are webp, riff,
vp8 and vp8l; the Unix adapter imports x/sys/unix. The separate current
`go list -deps ./cmd/dax-kiro-proxy` graph contains three external modules, with x/image absent.
That executable had only internal helper entry points at the audited `f931442` checkpoint. D35's
subsequent startup composition makes x/image reachable from the command too: a fresh
`go list -deps ./cmd/dax-kiro-proxy` now reports all four external application modules above. No module
version or license selection changed. These are package graphs, not proof of final linked symbols or
a future completed launcher's shipped contents.

D50's independent terminal observer also imports x/text/width v0.41.0 for text-cell widths, under
the same reviewed BSD-3-Clause LICENSE and PATENTS grant. This is test-only use; no module/version
was added and no upstream test corpus was imported. Offline go mod tidy moves the existing x/text
requirement from indirect to direct because this test imports it. Its Go 1.27 package selection includes
tables17.0.0.go, adding that generated Unicode data to the test artifact inventory. The existing
Unicode notice/release review below still applies; this observation does not close it.

The installed Go binary was invoked directly with GOTOOLCHAIN=local, GOPROXY=off, GOSUMDB=off and the
repository's existing module/build caches for these offline inspections. `go mod verify` reported
`all modules verified`. This compares cached module contents with retained hashes; it is not a fresh
online origin, advisory or toolchain authenticity check. Previously recorded checksum-database
verification and go.sum archive checksums remain separate evidence. The retained toolchain archive
ziphash is `h1:51Yfd9AJPm34szJ1qdVX7+kqAGDd3vI9FzVuY7UqLfA=` for
`golang.org/toolchain v0.0.1-go1.27.1.darwin-arm64`.

### Generated data, embedded resources and toolchain components

The installed x/text tables used by plural, language/compact, language and number packages identify
CLDR version 32. The standard-library vendored Unicode packages selected by the test graph use
tables17.0.0. A module's BSD declaration alone does not complete the attribution review for generated
data. The current [Unicode terms](https://www.unicode.org/copyright.html) identify the separate
[Unicode License v3](https://www.unicode.org/license.txt), subject to release-specific terms.
The exact CLDR 32 notice could not be fetched during this review. Determine and retain the applicable
data notices before closing the generated-component gate; no complete Unicode notice bundle exists
in this repository yet.

The validator's Go EmbedFiles metadata lists 19 metaschema resources covering drafts 04, 06, 07,
2019-09 and 2020-12. No separate license/NOTICE file or license marker was found under that embedded
resource directory. Record their provenance and applicable notices explicitly rather than assuming
the module's Apache label settles every embedded resource. The public
[JSON Schema 2020-12 core specification](https://json-schema.org/draft/2020-12/json-schema-core)
identifies IETF legal provisions; the release review must establish the applicable license and
notice for these particular embedded resources rather than inferring them from the specification
document's terms alone.

Whole dependency archives also contain material absent from the compiled graph. Attribution lines
in x/text's internal/testtext/text.go reference CC-BY-SA 3.0 Vietnamese and CC-BY-SA 1.0 Russian text.
That package is absent from this project's application/test dependency graph. Only attribution
markers were examined; the text was not copied as a fixture or used as implementation input. This
does not establish linked copyleft code, but it does prevent treating a bundle of entire module
caches or upstream test directories as BSD-only. Such bundling is not approved by this review.

Go's installed src/vendor/modules.txt records a separate standard-library vendor graph:

| Vendored module | Version recorded by Go 1.27.1 |
| --- | --- |
| golang.org/x/crypto | v0.52.1-0.20260526024921-9beb694f9766 |
| golang.org/x/net | v0.55.1-0.20260731170536-c1d18010be90 |
| golang.org/x/sys | v0.45.0 |
| golang.org/x/text | v0.37.0 |

The application/test graph reaches portions of vendored x/crypto, x/net and x/text. The current
command target reaches x/net's DNS package. The separately inspected src/cmd/vendor/modules.txt
contains additional build-tool dependencies and different versions; it must remain a separate
inventory from both this table and go.mod resolution. No claim that every Go toolchain component is
linked into the proxy follows from its presence in the toolchain archive.

Race-enabled test compilation selects runtime/race/race_darwin_arm64.syso. The installed race README
attributes that prebuilt runtime to LLVM revision 51bfeff0e4b0757ff773da6882f4d538996c9b04 with a Go
platform patch, built from Go revision a61fd428974822a8c57a2b2840fc237e6711b24d. Its measured SHA-256
is `6ca6a32e8b650ac03b6a11b40c7a0cdd938d00046bd1c942f7c530e0fdaf23b2`.
The [current LLVM license](https://llvm.org/LICENSE.txt) uses Apache-2.0 with LLVM exceptions and
identifies separately licensed components. Fetching the exact pinned compiler-rt license failed,
so that exact-component review remains open. Ordinary command compilation does not select the race
runtime; an instrumented binary or bundled toolchain would need its own distribution review.

### Advisory evidence and remaining release review

The official [GO-2026-5061 advisory](https://pkg.go.dev/vuln/GO-2026-5061) covers a WebP decoder panic
affecting Decode and DecodeConfig before x/image v0.43.0. The selected v0.45.0 is beyond that fixed
version. This checks one relevant advisory only. Database API requests did not succeed and no new
scanner was installed, so no complete dependency or standard-library vulnerability scan is claimed.
The [Go vulnerability documentation](https://go.dev/doc/security/vuln/) distinguishes curated reports
from function-reachability analysis; review and pin any scanner before adoption, then retain its
database date, target, toolchain, command and findings.

Before release, the concrete remaining dependency work was, at this checkpoint (D121 later completes
the first four items for the darwin/arm64 development artifact, except the three unmatched
metaschema resources, and moves the fifth outside the repository):

- resolve the generated Unicode/CLDR, embedded metaschema and exact race-runtime notices above;
- finish the appropriate build/test/toolchain component inventory, including target-specific native
  components, rather than counting only modules in go.sum;
- retain approved license and attribution texts with the artifact and inspect the actual binary,
  native dependencies, build metadata and archive contents for each supported target;
- run the reviewed advisory/reachability check against the selected toolchain and final package
  graph, and resolve findings without claiming that a clean result covers unknown vulnerabilities;
- close the owner's ownership/employment, specification-provenance, Kiro/private-extension use and
  intended-distribution records before selecting the project license or clearing release.

No release notice files, dependency examples, upstream fixtures or project LICENSE are introduced
by this checkpoint. Kiro, Claude Code and system test utilities remain separately installed
executables; their bundling and service/use permissions are not granted by these library reviews.

## Exact data and race-runtime notices — 2026-09-09

Two previously unavailable notice texts have now been obtained from their official release sources,
read in full, and retained byte-for-byte. This closes the missing-text portion of their reviews;
it does not complete the artifact, full subcomponent, advisory or owner-rights gates. The new notice
directories contain dependency licenses only, not a license for this project or bundled dependency
implementations, examples or fixtures.

### CLDR 32 generated data

The official [CLDR 32 release directory](https://www.unicode.org/Public/cldr/32/) supplies core.zip.
Its readme identifies the final CLDR 32 release and assigns data/software to unicode-license.txt.
The full 2,849-byte text matches
[Unicode-DFS-2016](https://spdx.org/licenses/Unicode-DFS-2016.html), with the original 1991-2017
copyright notice. It permits use and distribution while requiring the notice with the data/software
or associated documentation and restricting use of copyright-holder names for promotion. The current
Unicode License v3 must not silently replace this release-specific text.

The archive was fetched over HTTPS with a 25 MiB download ceiling and a forty-second deadline.
Measured size: 20,248,715 bytes. SHA-256:
`e561d24a93fac8ece726c3124e8ea5162b47da9b3caea58e0f78a896d33f5d1e`.
Only the release readme and named license files were extracted/read; no locale corpus, tool source
or fixture is used as implementation input. The complete archive remains in the ignored review
cache and is not a distribution input.

The data notice is retained at
[third_party/notices/runtime/unicode-cldr-32.txt](third_party/notices/runtime/unicode-cldr-32.txt),
SHA-256 `6a6976a5da6ac21a6a001c685c057f4a6e619be11f5465371088ecd80c7fbe06`.
It is byte-identical to the separate official
[CLDR 32.0.1 tag notice](https://raw.githubusercontent.com/unicode-org/cldr/release-32-0-1/unicode-license.txt)
also fetched in this review. The provenance of the retained file is core.zip for version 32, not an
assumed substitution of 32.0.1 data. x/text's generated CLDR-32 tables remain the scoped application
use identified in the previous checkpoint.

The release readme separately assigns ICU and Apache notices to ICU/Guava/Xerces dependencies of its
Java tools. Those tools are not used or bundled here. Their presence in a data release archive does
not establish that those libraries are linked into this Go application. They are not newly adopted
dependencies, and no complete review of ICU's separate bundled-data notices is claimed. Unicode 17
data in Go's standard-library vendor graph remains a distinct notice/provenance task.

### Exact Go race-runtime upstream license

The installed Go runtime/race README identifies LLVM revision
51bfeff0e4b0757ff773da6882f4d538996c9b04 for race_darwin_arm64.syso, as recorded above. A bounded
HTTPS fetch of that revision's
[compiler-rt LICENSE.TXT](https://raw.githubusercontent.com/llvm/llvm-project/51bfeff0e4b0757ff773da6882f4d538996c9b04/compiler-rt/LICENSE.TXT)
now succeeds. The entire 16,708-byte text was read. It identifies Apache-2.0 with LLVM exceptions,
separately licensed third-party components, and legacy NCSA/MIT alternatives for historical code.
The legacy paragraph is not treated as an unrestricted MIT election for all current LLVM material.

The exact text is retained at
[third_party/notices/test/llvm-compiler-rt.txt](third_party/notices/test/llvm-compiler-rt.txt),
SHA-256 `1a8f1058753f1ba890de984e48f0242a3a5c29a6a8f2ed9fd813f36985387e8d`.
It supports the recorded local race-testing use. This project has not modified upstream LLVM code.
Go's platform patch/build provenance, any applicable separately licensed sanitizer components and
final instrumented-artifact attribution remain separate from merely obtaining this top-level text.
The ordinary proxy binary does not select the race runtime; this test notice must not imply that
LLVM is an ordinary runtime dependency or authorize bundling the Go toolchain.

### Metaschema license clarification still awaiting resource matching

The current official JSON Schema specification
[README](https://raw.githubusercontent.com/json-schema-org/json-schema-spec/main/README.md) expressly
offers BSD-3-Clause or AFL-3.0, and its complete
[LICENSE](https://raw.githubusercontent.com/json-schema-org/json-schema-spec/main/LICENSE) was read.
The draft-bhutton-json-schema-01 release tag's README also states an AFL-or-BSD choice, but that tag
has no LICENSE at the attempted root path. No release-specific text was invented from the 404.

This is evidence that the specification source's license is not determined solely by the IETF prose
notice. It is not yet a completed mapping of all nineteen resources embedded by jsonschema v6.0.3.
Their draft-04/06/07/2019-09/2020-12 origins, exact resource correspondence, copyright notice and
chosen branch still need explicit matching before a complete metaschema notice is retained. No
upstream schema test suite was opened or adopted, no dependency version changed, and no claim of
complete distribution clearance follows from these two newly retained notices.

## Artifact and embedded-resource snapshot — D78, 2026-09-09

The tracked [development inventory](third_party/inventory/macos-arm64-development.json) now ties
the exact D77 darwin/arm64 executable to four external modules, retained notice hashes, the
selected command package graph and nineteen embedded metaschemas. The binary was rebuilt from
clean revision `15393c470f34970b2d893f313008a9321f8fe611` with Go 1.27.1; its embedded build metadata
reports `vcs.modified=false`. Its SHA-256 is
`b37127d909e617127448329994b5b278b8618f9ee08d0b196e14deafba4ebca5`.
This is an identified local development artifact, not a release or installation package. A later
rebuild may have different bytes even if dependency versions stay unchanged.

Offline `go list -mod=readonly -deps -json ./cmd/dax-kiro-proxy` selects 265 packages and the same
four external module versions/checksums present in the binary's Go build metadata: jsonschema
v6.0.3, x/image v0.45.0, x/sys v0.47.0 and x/text v0.41.0. The report separately lists fifteen
standard-library vendor packages and selected native source/object filenames. These are package
selection facts, not a symbol-level/native-library attribution audit. The report does not cover
every resolved-but-uncompiled module, test binary, build tool or future target.

The complete reviewed Go BSD notice, Go PATENTS grant and validator Apache-2.0 text are now retained
under third_party/notices/runtime. The Go/toolchain and three selected x/* modules have byte-identical
top-level LICENSE/PATENTS files; the inventory maps each exact version to both its cache file hash
and the shared retained text. No dependency implementation, example or upstream fixture is copied.
Existing CLDR-32 and test-only LLVM notices stay separate. The snapshot retains eight notice/reference
files in total; retaining them does not mean all eight are runtime licenses or that attribution is
complete. Top-level notice review is distinct from embedded/generated/component review.

### Metaschema correspondence

The [metaschema inventory](third_party/inventory/metaschemas.json) records all nineteen current
Go EmbedFiles entries, byte counts, raw SHA-256, sorted-key JSON hashes, official resource URLs,
five fixed specification tag commits, and exact JSON-pointer differences. Comparison ignores
object key order and insignificant JSON whitespace, but preserves array order and string values;
it performs no Unicode normalization and is not RFC 8785 canonicalization. All 57 compared JSON
documents (embedded, published and tag versions) separately pass duplicate-name/nonfinite-number
rejection. Structural equality is not a general proof of schema behavioral equivalence or ownership.

| Comparison | Result |
| --- | --- |
| Exact bytes versus current published resources | 0 of 19 |
| Structural JSON equality versus current published resources | 3 of 19 |
| Structural JSON equality versus selected fixed tag resources | 15 of 19 |
| Structural equality versus at least one of those sources | 16 of 19 |

Draft-07 and the two modern root schemas equal their current published counterparts. Most modern
vocabulary schemas instead equal the earlier official tag content, including its vocabulary
declaration. Draft-06 equals its selected historical tag. Differences from current publication
must therefore not automatically be attributed to the validator author or this project. The
remaining draft-04, 2019-09 applicator and 2019-09 core variants differ from both inspected sources;
their complete differences are recorded without inventing who changed them or why. No dependency
version or embedded resource is modified by this audit.

The four retrieved historical README files explicitly offer AFL or BSD for repository source
material and describe the metaschema files. The draft-04 README paths and attempted root LICENSE
paths did not yield usable texts; an HTTP failure is not proof that no licensing exists elsewhere.
The current dual-license text is retained, unchanged, as a **reference** from official commit
`4f56a9900674b27804f0ec32e3b7fdfa4efad695`:
[JSON Schema LICENSE](https://raw.githubusercontent.com/json-schema-org/json-schema-spec/4f56a9900674b27804f0ec32e3b7fdfa4efad695/LICENSE).
Its 2022 copyright line is preserved, without backdating it to historical drafts. Historical
applicability, attribution and the final dependency-license branch record remain open. Neither the
validator's Apache declaration nor the IETF prose notice substitutes for that resource review.

### Unicode 17 notice linkage

The official [Unicode 17 UCD archive](https://www.unicode.org/Public/17.0.0/ucd/UCD.zip) is 9,101,877
bytes, SHA-256 `2066d1909b2ea93916ce092da1c0ee4808ea3ef8407c94b4f14f5b7eb263d28e`.
Only the named release README and archive-entry names relevant to licensing were inspected; no
Unicode corpus/test data is used as an implementation fixture. The archive has no separate filename
containing "license". Its README identifies final Unicode 17 data and directs readers to the
[terms of use](https://www.unicode.org/copyright.html). Those terms apply Unicode License v3 to
data/software unless a specific release/material says otherwise.

Retain the complete release README attribution and current
[Unicode License v3](https://www.unicode.org/license.txt), including its published 1991-2026 year.
The latter is 1,995 bytes, SHA-256
`e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96`.
It is the currently referenced license text, not an invented version-frozen 2025 license file.
This provides notice/linkage evidence for the previously identified Unicode-17 tables; it does not
claim exhaustive table-generation, per-file exception or linked-artifact attribution review.

### Offline verification and remaining work

Verify the historical D78 report against the existing reviewed cache. Add `--binary` with a retained
copy of the exact D77 artifact to check that artifact as well; later development builds differ:

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot development
```

The tool performs no network request, executes no dependency and changes no file. It checks
go.mod/go.sum, retained notices, original cache notices, the metaschema report, all nineteen embedded
resource hashes and optionally the exact artifact. Paths, input sizes and file types are bounded;
symlinks/FIFOs are not accepted as input files. Without `--binary`, it verifies only snapshot/cache
bytes. Success always reports `release_clearance:false`; it does not discover new dependencies,
recompute current-source reachability or replace a license decision. New dependency versions or
an artifact change require a new reviewed snapshot, not editing a hash merely to obtain success.

The recorded invocation passes forty file-record checks including the binary. Nine independent
negative controls reject missing/changed input, oversized input, parent/absolute paths, input symlinks,
escaping directory links, FIFO and a forged release-clearance claim. Direct invocation of the
installed pinned Go binary with GOTOOLCHAIN=local, GOPROXY=off and GOSUMDB=off passes `go mod verify`.
An initial invocation through the automatic toolchain selector stops because that selector requires
checksum-database verification; it is not evidence of corrupt modules. No online authenticity or
advisory scan is implied by the successful cache-integrity check.

Public-only local Claude advice was saved, read in full and assessed. Its useful distinction between
content identity and rights does not make its assumptions evidence. In particular, a present-day
match cannot prove historical acquisition or absence of modifications. D121 later completes
component attribution, the advisory scan and the artifact inspection for the darwin/arm64
development artifact; clean macOS installation/uninstall remains release work; owner rights,
intended distribution and project license are outside this repository's scope. Local
packaging/installer implementation can continue without claiming that distribution is cleared.

## Installation artifact snapshot — D79, 2026-09-09

The new installer embeds exactly the seven already reviewed runtime/reference texts and excludes
the test-only LLVM notice. Installed-file tests compare all seven against the embedded bytes, retain
their runtime/reference directories and check their modes. This packaging step does not resolve the
historical JSON Schema applicability questions above or select a project license.

`third_party/inventory/macos-arm64-installation.json` identifies the new 13,572,242-byte command
artifact, SHA-256 `de7230ccb99bbe2fa8b011c7b89a6286734cebe9fc554310a0f05ea273e64e89`.
The Go 1.27.1 darwin/arm64 build has CGO_ENABLED=1 and reports D78's Git revision plus
`vcs.modified:true`. Its exact ninety selected first-party production Go files and seven embedded
texts are recorded alongside go.mod/go.sum, so the dirty build is not misrepresented as a clean
revision. Rebuilding later can change the artifact identity and needs its own evidence.

The newly collected command graph has 267 packages. Compared with D78's 265-package collection,
only the first-party installation and notices packages are added; none is removed. The four external
modules' versions, sums and selected package sets are identical. Existing dependency Go/native/embed
filename selections also match the previous collection. All retained notice and go.mod/go.sum bytes
match D78. This is a scoped graph/input comparison, not a new linked-symbol, toolchain provenance or
security-advisory analysis. The frozen D78 report remains unchanged and is itself hashed in D79.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot installation --binary dist/dax-kiro-proxy
```

This passes 138 file-record checks, including the exact artifact and prior report; some retained
texts participate as both source-embedding and notice records. The preserved D77 artifact still
passes the original forty checks. Both snapshots report `release_clearance:false`. The verifier
also rejects seven independently mutated owned fixtures: changed, missing or symlinked production
source; changed prior snapshot; false clearance; excessive source records; and an escaping path. It
selects a named frozen report, does not discover current dependencies and does not change files.
Unchanged external licensing evidence is carried forward explicitly; full historical-resource,
native/build/test attribution, advisory/reachability, clean-host release packaging and owner-rights
work remains open. No new dependency or client/model work is needed for this local installer.

## Native history artifact snapshot — D87, 2026-09-09

`third_party/inventory/macos-arm64-native-history.json` records the rebuilt 13,605,778-byte development
command, SHA-256 `7eb688d6a9e78d6c361ee6a89f1ff93dd458f0918a5b70b338c884333c529e46`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and D86's Git revision with vcs.modified=true.
Its 100 repository input records capture 91 selected production Go files, seven embedded notice
texts and go.mod/go.sum. The only added production input is launcher/client_history.go; main.go,
profile.go and startup.go are the three changed inputs relative to the frozen D79 report.

A fresh command-package collection matches D79's 267 import paths exactly. All four external module
versions, sums and selected package sets match, as do dependency Go/native/embed filename selections.
All eight retained notice/reference bytes and seven embedded notice files match. This carries the
scoped existing review forward; it does not repeat or complete licensing, advisory or native symbol
analysis. The D79 report remains unchanged and is hashed as this snapshot's predecessor. A retained
D79 executable remains under the ignored history-review directory; its source snapshot no longer
matches the current modified production files.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot native-history --binary dist/dax-kiro-proxy
```

The new record passes 139 checks. Like its predecessors, it explicitly reports release_clearance=false.
Native transcript retention introduces no new dependency. Historical embedded resources, complete
native/build/test attribution, security advisory/reachability review, clean-host distribution and
owner rights remain open.

## Immediate relay-close artifact snapshot — D96, 2026-09-09

`third_party/inventory/macos-arm64-relay-close.json` records the rebuilt 13,589,266-byte development
command, SHA-256 `56634fa10aacd2ea7335cab3abab9f3acdaf1db7d714f0630a778c02e496e951`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and parent c1212243445363ec2596108ed0b4c8b71cd4021f
with vcs.modified=true. The build captures the recorded uncommitted production inputs; it does not
claim a subsequent commit identity. The same 100 repository input paths remain selected: 91 Go
sources, seven embedded notice files and go.mod/go.sum. Only internal/relay/socket.go changes.

Fresh command metadata contains 267 packages and the same four external module versions, sums and
selected package sets as D87. Selected native filenames and stdlib vendor package names match too.
The new snapshot records the complete current import paths and per-package selected filenames;
D87's smaller record does not support claiming an independently compared full prior Go-file list.
Retained notice/reference and embedded notice bytes remain unchanged. This carries the existing
scoped review forward without a new dependency, fresh advisory review or native symbol analysis.

The D87 report is preserved and hashed as the predecessor. Its executable is retained under the
ignored history-review directory before replacing dist/dax-kiro-proxy. Historical snapshots no
longer match the changed source bytes. Verify the current artifact using:

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot relay-close --binary dist/dax-kiro-proxy
```

All 139 byte checks pass, with release_clearance=false. Historical embedded resources, complete
native/build/test attribution, advisory/reachability review, clean-host distribution and owner
rights remain open; the faster shutdown does not alter those obligations.

## Corrected effort artifact snapshot — D99, 2026-09-10

`third_party/inventory/macos-arm64-effort.json` records the rebuilt 13,589,298-byte development
command, SHA-256 `e5a32291b1f27d00bb4fff0fb63b26eb19e6c8324d0b7de88f2122fe07528f00`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and parent
5c8fd6b0bb4af62336c77d02f5fa5b002d8360f7 with vcs.modified=true. The captured production bytes
include the effort fix before its commit; this is not a later clean-commit artifact.

The same 100 repository input paths remain selected. Only internal/kirofeature/effort.go changes
against D96. All 267 import paths and selected filenames match D96, including the four external
module versions, sums and package sets, native selections and stdlib vendor package list. Retained
notice/reference and embedded notice bytes match. No dependency is added or upgraded. The D96
snapshot remains unchanged and is hashed as this snapshot's predecessor; its executable is retained
under the ignored history-review directory before replacing the development command.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot effort --binary dist/dax-kiro-proxy
```

The rebuilt candidate passes all 139 byte checks and release_clearance remains false. This carries
forward the existing scoped dependency review; advisory/reachability review, complete native/build/
test attribution, clean-host distribution and owner rights remain open. Correcting the private
command does not establish private-extension licensing permission or release clearance.

## Account usage artifact snapshot — D101, 2026-09-10

`third_party/inventory/macos-arm64-usage.json` records the rebuilt 13,624,658-byte development
command, SHA-256 `d9c786248f3b7c90de0e3ecc546bd2365ffff4740754eb3ea432774894136bac`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and parent
8ba2f00ac7dfa1c435222470217b454471cca3f4 with vcs.modified=true. It captures the uncommitted
D101 production inputs rather than claiming a subsequent clean-commit build.

There are 102 repository input records: 93 selected Go files, seven embedded notice/reference
files and go.mod/go.sum. Two usage adapters are added; ACP initialization, launcher startup/runtime
and the usage cache account for five changed source files. The 267 import paths, four external
module versions/sums/package sets, native selections and stdlib vendor packages match D99. Only
the two adapter packages gain selected filenames. Retained notices and reviewed dependency bytes
are unchanged. No dependency is added or upgraded. D99 remains the hashed predecessor, with its
executable retained before replacement.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot usage --binary dist/dax-kiro-proxy
```

All 141 byte checks pass with release_clearance false. Existing advisory/reachability, native/build/
test attribution, clean-host distribution and owner-rights work remain open. The read-only private
usage observation is interoperability evidence and does not grant private-extension license rights.

## Output-style artifact snapshot — D108, 2026-09-10

`third_party/inventory/macos-arm64-output-styles.json` records the rebuilt 13,624,658-byte development
command, SHA-256 `34bd1751b9055b0f68bc4378d446d8b15578e0540332282d1a93f614b67da9ab`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and parent
f1cae2cc1b0f07102566f3bc66cd86872ba9b75c with vcs.modified=true. The record captures the uncommitted
D108 production inputs; it does not claim that the artifact was built from the subsequent commit.

The 102 repository input records remain 93 selected Go files, seven embedded notice/reference files
and go.mod/go.sum. Only internal/launcher/client_customizations.go changes from D101. The 267 import
paths, four external module versions/sums/package sets, selected native files, stdlib vendor packages
and retained notices are unchanged. No dependency is added or upgraded. D101's usage inventory is
the hashed predecessor, and its executable is retained before replacement.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot output-styles --binary dist/dax-kiro-proxy
```

All 141 byte checks pass against the published development artifact with release_clearance false.
Existing advisory/reachability, native/build/test attribution, clean-host distribution and owner-rights work remain
open. Preserving a user's style files and observing public client behavior grants no additional
license or distribution rights.

## Tool-image history artifact snapshot — D109, 2026-09-10

`third_party/inventory/macos-arm64-tool-images.json` records the rebuilt 13,625,026-byte development
command, SHA-256 `5c8cd3d2661e907d8b3fb05cdb6e18e8fb60c03b931ac5131e1eaf85ddc49971`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and parent
86ec14b90c7715ea9b263fb8091ea7c96e60a2e6 with vcs.modified=true. It captures the uncommitted D109
production inputs rather than claiming a subsequent clean-commit build.

There are still 102 repository inputs: 93 selected Go files, seven notice/reference files and
go.mod/go.sum. Only the Anthropic client-tools and projection media/text files change from D108.
The 267 import paths, four external module versions/sums/package sets, selected filenames/native
files, stdlib vendor packages and retained notices are unchanged. No dependency is added or
upgraded. D108's output-style inventory is the hashed predecessor.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot tool-images --binary dist/dax-kiro-proxy
```

All 141 byte checks pass against the candidate with release_clearance false. Existing advisory/
reachability, native/build/test attribution, clean-host distribution and owner-rights work remain
open. The synthetic PNG and independent protocol cases are authored within this repository; no
image fixture or implementation is imported from another project.

## Client-admission artifact snapshot — D110–D112, 2026-09-10

`third_party/inventory/macos-arm64-client-version.json` records the rebuilt 13,641,842-byte development
command, SHA-256 `590d7c6acfe743cd3cb5921f752ef23e371d030c5e3df325c91f212fc410e64c`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision
93182c901262f9ee5bd56c015b678e658cd9bca5 with vcs.modified=false; the inventory commit follows that
revision rather than capturing uncommitted inputs.

The 102 repository input records remain 93 selected Go files, seven notice/reference files and
go.mod/go.sum. Four production files change from D109: the command's doctor rendering and the
launcher's client_mcp.go, profile.go and startup.go. The 267 import paths, four external module
versions/sums/package sets, selected native files, stdlib vendor packages and retained notices are
unchanged. No dependency is added or upgraded. D109's tool-image inventory is the hashed
predecessor; the D109, D110/D111 and D112 executables are retained under the ignored history-review
directory.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot client-version --binary dist/dax-kiro-proxy
```

All 141 byte checks pass against the candidate with release clearance false. Existing advisory/
reachability, native/build/test attribution, clean-host distribution and owner-rights work remain
open. Admitting a client build by major version and opting it out of a later build's window
enforcement grant no license or distribution right and do not change the reviewed dependency set.

## Measured-client artifact snapshot — D113, 2026-09-11

`third_party/inventory/macos-arm64-measured-client.json` records the rebuilt 13641842-byte development
command, SHA-256 `840b83337a385609ef361cefacacff0935cc98b522fd7b12a86d4b601008adba`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision
e6f96e9c8903b9522ee7f71743983669e851ece0 with vcs.modified=false; the inventory commit follows that
revision rather than capturing uncommitted inputs.

The 102 repository input records remain 93 selected Go files, seven notice/reference files and
go.mod/go.sum. One production file changes from D112: the launcher's profile.go, whose measured
client version constant moves from 2.1.263 to 2.1.267. The 267 import paths, four external module
versions/sums/package sets, selected native files, stdlib vendor packages and retained notices are
unchanged. No dependency is added or upgraded. The D110–D112 client-version inventory is the hashed
predecessor; the D112 and D113 executables are retained under the ignored history-review directory.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot measured-client --binary dist/dax-kiro-proxy
```

All 141 byte checks pass against the candidate with release clearance false. Existing advisory/
reachability, native/build/test attribution, clean-host distribution and owner-rights work remain
open. Moving the measured client pin grants no license or distribution right and does not change
the reviewed dependency set.

## Measured-Kiro artifact snapshot — D114, 2026-09-11

`third_party/inventory/macos-arm64-measured-kiro.json` records the rebuilt 13641922-byte development
command, SHA-256 `c09963175e039195c87959edba9410b6f170e540866efc58e1cde0e9bd03a81d`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision
fe71d19e614b259365cbd3cc15a968323076c54d with vcs.modified=false; the inventory commit follows that
revision rather than capturing uncommitted inputs.

The 102 repository input records remain 93 selected Go files, seven notice/reference files and
go.mod/go.sum. Seven production files change from D113: the command's doctor rendering, the
kirofeature usage comment, and the launcher's kiro.go, kiro_execution.go, kiro_usage.go, profile.go
and startup.go for Kiro major-version admission and the measured 2.21.3 pin. The 267 import paths,
four external module versions/sums/package sets, selected native files, stdlib vendor packages and
retained notices are unchanged. No dependency is added or upgraded. The D113 measured-client
inventory is the hashed predecessor; the D113 and D114 executables are retained under the ignored
history-review directory.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot measured-kiro --binary dist/dax-kiro-proxy
```

All 141 byte checks pass against the candidate with release clearance false. Existing advisory/
reachability, native/build/test attribution, clean-host distribution and owner-rights work remain
open. Admitting Kiro builds by major version and moving the measured Kiro pin grant no license or
distribution right and do not change the reviewed dependency set.

## Onboarding artifact snapshot — D115, 2026-09-11

`third_party/inventory/macos-arm64-onboarding.json` records the rebuilt 13641922-byte development
command, SHA-256 `720b6f6ec5812f9e5dff4365f7d97f72c6781e44e2ed69e82f544ff0e23f980c`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision
52b460b9e118dd248f2cc6e92af9d90f588b1221 with vcs.modified=false; the inventory commit follows that
revision rather than capturing uncommitted inputs.

The 102 repository input records remain 93 selected Go files, seven notice/reference files and
go.mod/go.sum. Two production files change from D114: the launcher's client_mcp.go, whose
projection carries the reviewed completed-onboarding flag, and profile.go, whose host environment
keeps only the Bearer token. The 267 import paths, four external module versions/sums/package sets,
selected native files, stdlib vendor packages and retained notices are unchanged. No dependency is
added or upgraded. The D114 measured-Kiro inventory is the hashed predecessor; the D114 and D115
executables are retained under the ignored history-review directory.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot onboarding --binary dist/dax-kiro-proxy
```

All 141 byte checks pass against the candidate with release clearance false. Existing advisory/
reachability, native/build/test attribution, clean-host distribution and owner-rights work remain
open. Removing the client's onboarding dialogs grants no license or distribution right and does not
change the reviewed dependency set.

## Workspace-trust artifact snapshot — D118, 2026-09-11

`third_party/inventory/macos-arm64-project-trust.json` records the rebuilt 13659058-byte development
command, SHA-256 `0b2f416b1a054935729e59f6e4c75eeeb1ccebd6f49aba59f46eaa5eb521e28d`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision
f522c4df90201de0b56b622d6286ed165e52e358 with vcs.modified=false; the inventory commit follows that
revision rather than capturing uncommitted inputs.

The 102 repository input records of 93 selected Go files, seven notice/reference files and
go.mod/go.sum grow to 103 records: the launcher's client_trust.go is added, and client_mcp.go,
profile.go and runtime.go change for the workspace-trust write-back. The 267 import paths, four
external module versions/sums/package sets, selected native files, stdlib vendor packages and
retained notices are unchanged. No dependency is added or upgraded. The D115 onboarding inventory
is the hashed predecessor; the D115 and D118 executables are retained under the ignored
history-review directory.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot project-trust --binary dist/dax-kiro-proxy
```

All 142 byte checks pass against the candidate with release clearance false. Existing advisory/
reachability, native/build/test attribution, clean-host distribution and owner-rights work remain
open. Recording the user's workspace-trust answer grants no license or distribution right and does
not change the reviewed dependency set.

## Component inventory completion and scope change — D121, 2026-09-11

The tracked [component record](third_party/inventory/components.json) consolidates every input of
the darwin/arm64 development artifact with exact versions, checksums, origins, purposes, scopes,
SPDX expressions, license/notice paths and digests, reviewer and date, chosen branches and status.
`python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --components` checks its 18
file records offline against `go.mod`, `go.sum`, the module cache, the retained notices and the
metaschema inventory; two negative controls reject an altered notice digest and a linked version
absent from `go.mod` (`d121-components-verify.log`). At the owner's direction the ownership,
provenance, policy, service-terms and distribution items of the rights checklist are handled outside
this repository from this date; the repository asserts no status for them and still selects no
project license.

| Component | Exact identity | Scope | License evidence | Status |
| --- | --- | --- | --- | --- |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.3, `h1:1EYB5Izj…` | linked; command and test graphs | Apache-2.0, cached LICENSE `c8858a5a…` = retained `jsonschema-apache-2.0.txt` | approved for development and local installation |
| golang.org/x/image | v0.45.0, `h1:FMb1nTbH…` | linked (riff, vp8, vp8l, webp) | BSD-3-Clause + PATENTS, cached files = retained `go-bsd.txt`/`go-patents.txt` | approved |
| golang.org/x/sys | v0.47.0, `h1:o7XGOvZQ…` | linked (unix) | BSD-3-Clause + PATENTS, same retained texts | approved |
| golang.org/x/text | v0.41.0, `h1:vz/seA0l…` | linked (message, language, plural and internals; width in tests) | BSD-3-Clause + PATENTS, same retained texts; CLDR 32 data under `unicode-cldr-32.txt` | approved |
| github.com/dlclark/regexp2 | v1.11.0 | resolved graph only, never compiled | MIT, cached LICENSE `9be5d04b…` | not a dependency of any built artifact |
| golang.org/x/mod, x/sync, x/tools | v0.38.0, v0.22.0, v0.48.0 | resolved graph only; archives never downloaded | x/sync public LICENSE read; others not reviewed | not a dependency of any built artifact |
| golang.org/toolchain | v0.0.1-go1.27.1.darwin-arm64, ziphash `h1:51Yfd9AJ…` | build/test toolchain, standard library and runtime | LICENSE/PATENTS byte-identical to the retained Go notices; vendored x/crypto, x/net, x/sys v0.45.0, x/text v0.37.0; Unicode 17 notices retained | not bundled |
| runtime/race/race_darwin_arm64.syso | 502,032 bytes, `6ca6a32e…` | race-enabled tests only | Apache-2.0 WITH LLVM-exception text retained under `notices/test` | not in the ordinary or installed executable |
| Embedded metaschemas | 19 resources of jsonschema v6.0.3 | compiled into the validator | specification offer BSD-3-Clause OR AFL-3.0; chosen branch BSD-3-Clause; reference text retained | 16 matched to specification sources; draft-04 root, 2019-09 applicator and core unattributed |
| Apple system libraries | libSystem.B, libresolv.9, CoreFoundation, Security | dynamic links of the installed binary (`otool -L`) | Apple macOS; SDK and signing terms outside this review | not distributed |
| Build environment | macOS 15.4, Command Line Tools, MacOSX 15.5 SDK, Apple clang 17.0.0, CGO_ENABLED=1, ad hoc linker signature | build host | outside this review | nothing from the SDK is bundled |
| Test utilities | python3 3.11.14, expect 5.45, script, ps, lsof, pgrep, Apple Git 2.39.5 | test host executables | host-provided | neither linked nor bundled |
| External executables | kiro-cli 2.21.3, Claude Code 2.1.269 (D126; 2.1.268 from D124) | black-box backend and client | service terms are the owner's external item | neither linked nor bundled |
| Advisory scanner | govulncheck v1.8.0 (`golang.org/x/vuln`, `h1:clG4qBU6…`) with x/mod v0.41.0, x/sync v0.23.0, x/telemetry 20260908, x/tools v0.50.0 | session scratch GOPATH only | BSD-3-Clause, LICENSE byte-identical to Go's | not a repository dependency |

Advisory results (database `https://vuln.go.dev` updated 2026-09-10T14:48:42Z, Go 1.27.1): source
mode over `./...`, source mode with test packages, and binary mode over the installed D118
executable (SHA-256 `0b2f416b1a054935729e59f6e4c75eeeb1ccebd6f49aba59f46eaa5eb521e28d`) each report
"No vulnerabilities found." (`d121-govulncheck-install.log`, `d121-govulncheck-source.log`,
`d121-govulncheck-source-test.log`, `d121-govulncheck-binary.log`). This covers curated reports with
symbol-level reachability for Go code as of that database date and makes no statement about unknown
vulnerabilities, the Apple libraries or the separately installed executables. Repeat the scan and
record its database date whenever a dependency, toolchain or artifact changes.

Artifact inspection: the installed generation's manifest lists the executable and seven notice files
whose digests equal the repository's `third_party/notices/runtime` and `reference` texts; the
test-only LLVM text is not installed by design. The binary's build metadata reports revision
`f522c4d`, `vcs.modified=false`, GOARM64 v8.0 and empty CGO flags. No archive or package form
exists; the per-user generation directory is the inspected distribution unit. Remaining within
scope: the three unattributed metaschema resources, a notice bundle for any future non-macOS target,
and repeating this inventory on change. The `project-trust` snapshot's 142 byte checks against the
installed binary continue to pass.

## Run-diagnostics artifact snapshot — D122, 2026-09-12

`third_party/inventory/macos-arm64-run-diagnostics.json` records the rebuilt 13,676,354-byte
development command, SHA-256 `59e8eb4b7369102cba08187d5b6aaf55733fb69c5e7923aea10ee5643a579a5e`. It
identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision eab2bc4a37e9 (the
D122 code commit plus its review fixes) with vcs.modified=false; the inventory commit follows that
revision rather than capturing uncommitted inputs.

The 103 repository input records are unchanged in number: ten production files change from D118 (the
command's signal handling, options and diagnostics; the launcher's kiro.go, profile.go and
startup.go; childproc's attached.go and terminal_unix.go; the gateway's error mapping; the schema
pool and its worker; the session continuation). The 267 import paths form the same set as D118; the
gateway now imports the schema-check package directly, which reorders the dependency listing
(`import_path_order_changed_since_D118`). The four external module versions/sums/package sets,
selected native files, stdlib vendor packages and retained notices are unchanged. No dependency is
added or upgraded. The D118 project-trust inventory is the hashed predecessor; the D118 and D122
executables are retained under the ignored history-review directory. The D121 component record needs
no change.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot run-diagnostics --binary dist/dax-kiro-proxy
```

All 142 byte checks pass against the candidate with release clearance false. Existing
advisory/reachability, native/build/test attribution and clean-host distribution work remain open;
the owner's rights items are outside this repository (D121). Bounding the session lifetime, handling
SIGHUP, naming failures and queuing schema checks grant no license or distribution right and do not
change the reviewed dependency set.

## Deferred-standing artifact snapshot — D123, 2026-09-12

`third_party/inventory/macos-arm64-deferred-standing.json` records the rebuilt 13,676,354-byte
development command, SHA-256 `20a814689b10b18a90f9e3fbb3971cec4fd49c2eb21f25a4adf8e6e4fc92b71d`. It
identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision c8b293dec0c7 (the
D123 code commit plus its review fixes) with vcs.modified=false; the inventory commit follows that
revision rather than capturing uncommitted inputs.

The 103 repository input records are unchanged in number: three production files change from D122,
the session package's continuation.go, continuation_restart.go and interruption.go. The 267 import
paths, the four external module versions/sums/package sets, selected native files, stdlib vendor
packages and retained notices are unchanged. No dependency is added or upgraded. The D122
run-diagnostics inventory is the hashed predecessor; the D122 and D123 executables are retained
under the ignored history-review directory. The D121 component record needs no change.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot deferred-standing --binary dist/dax-kiro-proxy
```

All 142 byte checks pass against the candidate with release clearance false. Existing
advisory/reachability, native/build/test attribution and clean-host distribution work remain open;
the owner's rights items are outside this repository (D121). Deferring a rotated standing
instruction grants no license or distribution right and does not change the reviewed dependency set.

## Measured-client-268 artifact snapshot — D124, 2026-09-12

`third_party/inventory/macos-arm64-measured-client-268.json` records the rebuilt 13,676,354-byte
development command, SHA-256 `282b8556b6feb1dd5cf0d8082b67bbe8e470ac9644a30af14cf8f20e252eb9b6`. It
identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and the clean committed revision e08ce8eb25af (the
D124 commit) with vcs.modified=false; the inventory commit follows that revision rather than
capturing uncommitted inputs. The byte count equals D123's because the only production change
replaces the measured client version string with one of the same length.

The 103 repository input records are unchanged in number: one production file changes from D123, the
launcher package's profile.go (`SupportedClientVersion` 2.1.267 → 2.1.268). The 267 import paths,
the four external module versions/sums/package sets, selected native files, stdlib vendor packages
and retained notices are unchanged. No dependency is added or upgraded. The D123 deferred-standing
inventory is the hashed predecessor; the D123 and D124 executables are retained under the ignored
history-review directory. The D121 component record's client entry and the table row above now name
2.1.268 as the measured pin; nothing else in that record changes.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot measured-client-268 --binary dist/dax-kiro-proxy
```

All 142 byte checks pass against the candidate with release clearance false, and the installed copy
under `~/.local/bin` is byte-identical to it. Existing advisory/reachability, native/build/test
attribution and clean-host distribution work remain open; the owner's rights items are outside this
repository (D121). Measuring a client build grants no license or distribution right and does not
change the reviewed dependency set.

## Guarded trust-publication artifact snapshot — D125, 2026-09-12

`third_party/inventory/macos-arm64-trust-publication.json` records the rebuilt 13,677,298-byte
development command, SHA-256 `9088ebaf799e3a844d0499e174e77f4ca17f52992bf22e3aa2267c7d7b248179`. It
identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and clean committed revision `c418f13` with
`vcs.modified=false`, including the independent review's final HOME check. The inventory commit
follows the code commit. The original `c67627a` candidate (SHA-256 `5e387757…`) and its inventory
are retained under `.cache/history-review/*d125-before-review*`.

There are 104 repository input records: `internal/launcher/client_trust.go` changes and
`internal/launcher/client_trust_write.go` is added relative to D124. The 267 import paths, four
external module versions/sums/package sets, selected native files, stdlib vendor packages and
retained notices are unchanged. The D124 measured-client-268 inventory is the hashed predecessor.
No dependency is added or upgraded, and the D121 component record still passes its 18 checks.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot trust-publication --binary dist/dax-kiro-proxy
```

All 143 byte checks pass against both the candidate and the resolved installed executable, with
release clearance false (`d125-installed-verify-final.log`). `install --force` succeeds
(`d125-install-final.log`). The installed diagnostic
reports Kiro 2.21.3 measured and Claude Code 2.1.269 unmeasured: the host updated its client again;
the tested client pin remains 2.1.268. Login and the development execution policy pass, and launch
availability is true (`d125-installed-doctor-final.json`). This is no verification claim for
2.1.269.

The D121-reviewed govulncheck v1.8.0 reports no vulnerabilities in binary, source and source-with-test
modes on this artifact/source. Its database is updated 2026-09-10T14:48:42Z
(`d125-govulncheck-version.log`, `d125-govulncheck-binary-final.log`,
`d125-govulncheck-source-final.log`, `d125-govulncheck-source-test-final.log`). These scans cover
curated Go reports with symbol reachability
as of that database date, not unknown vulnerabilities, system libraries or separately installed
executables. The owner's rights determinations remain outside the repository under D121.

## Measured-client-269 artifact snapshot — D126, 2026-09-12

`third_party/inventory/macos-arm64-measured-client-269.json` records the rebuilt 13,677,298-byte
command, SHA-256 `cb5cb96abc53ca9d31242263ac105b18ac4685eb9e39efe41e3c1f6626474485`. It identifies
Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and clean committed revision `3302837` with
`vcs.modified=false`. The inventory commit follows that code commit.

Only `internal/launcher/profile.go` changes among the 104 production input records: the measured
client constant is 2.1.269. The 267 import paths, four external module versions/sums/package sets,
selected native files, stdlib vendor packages and retained notices are unchanged. D125's
trust-publication inventory is the hashed predecessor. No dependency is added or upgraded. The
D121 component record names 2.1.269 as the measured client; its 18 file checks still pass.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot measured-client-269 --binary dist/dax-kiro-proxy
```

All 143 byte checks pass against both the candidate and the resolved installed executable, with
release clearance false (`d126-freeze.log`, `d126-installed-verify.log`). `install --force` succeeds
(`d126-install.log`). The installed doctor reports Claude Code 2.1.269 and Kiro 2.21.3 measured,
login and policy verified, and launch available (`d126-installed-doctor.json`). This is development
admission; no actual Kiro model test ran for this client migration. D125's advisory scans remain
historical evidence for their recorded artifact/source, not new scans of this executable. The
owner's rights determinations remain outside the repository under D121.


## Progress and relay-setup artifact snapshot — D127, 2026-09-12

`third_party/inventory/macos-arm64-progress-setup.json` records the rebuilt 13,694,242-byte
development command, SHA-256 `1d9e080e63bd880917def5d2f429b7e1da840afce12ebfc23ad36ec3cec104af`.
It identifies Go 1.27.1, darwin/arm64, CGO_ENABLED=1 and clean committed revision
`7ce5a58dab37bb33d26ff233749a8827a020c764` with vcs.modified=false. The inventory commit follows
the code commit.

There are 105 repository input records. Seven production files change from D126:
gateway/http.go, inference/contract.go, relay/attachment.go, relay/attachment_child.go,
relay/socket.go, session/setup.go and session/turn.go, all under internal/. The new
internal/session/progress.go is the only added production input. The 267 import paths, four
external module versions/sums/package sets, selected native files, stdlib vendor packages and
notices are unchanged. D126 measured-client-269 is the hashed predecessor; its artifact remains
retained. No dependency is added or upgraded.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot progress-setup --binary dist/dax-kiro-proxy
```

All 144 byte checks pass for the candidate and resolved installed executable; the 18 component
checks pass too. Install --force succeeds, and doctor reports measured Kiro 2.21.3/Claude
2.1.269, verified login/policy and launch_available true. The snapshot retains release_clearance
false. D125 advisory results remain historical; this batch performs no fresh advisory scan or
actual Kiro model turn. Independent review accepts D127 after the two record corrections at
`220f094`, with no production input changes. The reviewer independently confirms the 144 byte
checks for both candidate and installed executables and all 18 component checks; no refreeze is
needed.


## Resolved relay cancellation artifact snapshot — D128, 2026-09-13

`third_party/inventory/macos-arm64-resolved-cancellation.json` records the rebuilt
13,694,242-byte development command, SHA-256
`3f4d9841458407612395bbc06fa2aa113ed0b7e1602447e890f0213c72a81d6d`. It identifies Go 1.27.1,
darwin/arm64, CGO_ENABLED=1 and clean revision `4494910a4f996b62b867cb12bcb4b2d6e12d04bf` with
vcs.modified=false. D127 progress-setup is the hashed predecessor and remains retained.

Only `internal/relay/broker.go` changes among the same 105 production input records. The 267
import paths, selected files, four external module versions/sums/package sets, native files,
stdlib vendor packages and notices are unchanged. No dependency is added or upgraded.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot resolved-cancellation --binary dist/dax-kiro-proxy
```

All 144 byte checks pass for the candidate and resolved installation, and all 18 component checks
pass. Install --force succeeds; doctor reports the measured Kiro 2.21.3/Claude 2.1.269 pair,
verified login/policy and launch available. Release clearance remains false. D125's advisory
results remain historical; D128 performs no new scan or actual Kiro model turn. Independent
review accepts `7e28df7` without a finding and independently confirms candidate/installed byte
identity and the component record. No refreeze is needed.


## Retained tool outcome artifact snapshot — D129, 2026-09-13

`third_party/inventory/macos-arm64-tool-outcomes.json` records the rebuilt 13,694,546-byte
development command, SHA-256
`d723495ec219dda7e2a465ccfd06adaed24fe5e95412290ac54dbd671b529b62`. It identifies Go 1.27.1,
darwin/arm64, CGO_ENABLED=1 and clean revision `dc119e7d7ae2a479afde7e6f61f4687114d0f1ea` with
vcs.modified=false. D128 resolved-cancellation is the hashed predecessor and remains retained.

Only `internal/session/continuation.go` and `internal/session/turn.go` change among the same
105 production input records. The 267 import paths, selected files, four external module
versions/sums/package sets, native files, stdlib vendor packages and notices are unchanged.
No dependency is added or upgraded.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot tool-outcomes --binary dist/dax-kiro-proxy
```

All 144 byte checks pass for the candidate and resolved installation (`d129-freeze.log`,
`d129-installed-verify.log`), and all 18 component checks pass (`d129-components.log`). Install
--force succeeds (`d129-install.log`); doctor reports the measured Kiro 2.21.3/Claude 2.1.269
pair, verified login/policy and launch available (`d129-installed-doctor.json`). Client
initialization remains unverified by doctor. Release clearance remains false. D125's advisory
results remain historical; D129 performs no new scan or actual Kiro model turn. Independent
review accepts `3065fd2` without an actionable finding and confirms the candidate/installed byte
identity and component record. No refreeze is needed. The owner's rights determinations stay
outside this repository under D121.


## Prompt stop-reason artifact snapshot — D130, 2026-09-13

`third_party/inventory/macos-arm64-prompt-stop.json` records the rebuilt 13,694,546-byte
development command, SHA-256
`6538b2e84bc6032d05235e2ff67eeca6eb7f36c3960954a73119eaad7527796c`. It identifies Go 1.27.1,
darwin/arm64, CGO_ENABLED=1 and clean revision `3d35c515a3403f2fafb337540cadf734183506c1` with
vcs.modified=false. D129 tool-outcomes is the hashed predecessor and remains retained.

Only `internal/gateway/http.go` and `internal/session/turn.go` change among the same 105
production input records. All 267 import paths, selected files, four external module versions/
sums/package sets, native files, stdlib vendor packages and notices remain unchanged. No
dependency is added or upgraded.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot prompt-stop --binary dist/dax-kiro-proxy
```

Candidate and resolved installed binaries each pass all 144 byte checks (`d130-freeze.log`,
`d130-installed-verify.log`); all 18 component checks pass (`d130-components.log`). Install
--force succeeds (`d130-install.log`). The first doctor fails login_check after 5.137s without a
JSON result; its cause is unestablished (`d130-installed-doctor-timing.log`). An unchanged
read-only retry verifies the measured pair, login/policy and launch availability
(`d130-installed-doctor-retry.json`). Doctor client initialization remains unverified and release
clearance remains false. D125 advisory scans remain historical; no new scan or actual Kiro model
turn ran. Independent review accepts `037ef3a` without an actionable finding and independently
confirms both 144-check artifact results, all 18 component checks and unchanged dependency
records. No production fix or refreeze is needed. Owner rights stay outside this repository
under D121.

## Account-check failure artifact snapshot — D131, 2026-09-13

`third_party/inventory/macos-arm64-account-check.json` records the rebuilt 13,694,866-byte
development command, SHA-256
`1490ac26e1363f59391b651acb1c06effa4534b6addb4df1893b15e653e02b9b`. It identifies Go 1.27.1,
darwin/arm64, CGO_ENABLED=1 and clean revision `497e0e11e704ca5392f598578c80efb7f586621e` with
vcs.modified=false. D130 prompt-stop is the hashed predecessor and remains retained.

Only `cmd/dax-kiro-proxy/main.go` and `internal/launcher/kiro.go` change among the same 105
production input records. All 267 ordered import paths, selected files, four external module
versions/sums/package sets, native files, stdlib vendor packages and notices remain unchanged.
No dependency is added or upgraded.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot account-check --binary dist/dax-kiro-proxy
```

Candidate and strictly resolved installed binaries each pass all 144 byte checks
(`d131-freeze.log`, `d131-installed-verify.log`); all 18 component checks pass
(`d131-components.log`). Install --force succeeds (`d131-install.log`). Doctor passes on its
first invocation, verifying measured Kiro 2.21.3/Claude 2.1.269, login/policy and launch
availability (`d131-installed-doctor.json`). Client initialization remains unverified and
release clearance remains false. D125 advisory scans remain historical; no new scan or actual
Kiro model turn ran. Independent review accepts `ac71b58` without an actionable finding and
independently verifies candidate, installed and retained binaries (144 checks each), all 18
component checks, embedded build metadata and all 105 committed production inputs. Dependency
records remain unchanged. No production fix or refreeze is needed. Owner rights stay outside
this repository under D121.

## Declared tool schema artifact snapshot — D132, 2026-09-13

`third_party/inventory/macos-arm64-schema-dialects.json` records the rebuilt 13,694,946-byte
development command, SHA-256
`a101bd21d1a68e17b7f04f5207ea04109a021c4ff0be15aaf1a2bb018ca3736f`. It identifies Go 1.27.1,
darwin/arm64, CGO_ENABLED=1 and clean revision `cc297b40a23d8ed4b5287a696cff5513feb780b1` with
vcs.modified=false. D131 account-check is the hashed predecessor and remains retained.

Only `internal/schemawire/input.go` changes among the same 105 production input records. All
267 ordered import paths, selected files, four external module versions/sums/package sets,
native files, stdlib vendor packages and notices remain unchanged. No dependency is added or
upgraded. The existing reviewed validator now receives explicitly declared Draft 7 and 2019-09
schemas through the input gate as well as 2020-12; it keeps dialect selection and denied loading
inside the existing bounded worker. Its already-included resource inventory is unchanged.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot schema-dialects --binary dist/dax-kiro-proxy
```

Candidate and strictly resolved installed binaries each pass all 144 byte checks
(`d132-freeze.log`, `d132-installed-verify.log`); all 18 component checks pass
(`d132-components.log`). Install --force succeeds (`d132-install.log`). The first doctor
invocation verifies measured Kiro 2.21.3/Claude 2.1.269, login/policy and launch availability
(`d132-installed-doctor.json`). Client initialization remains unverified; release clearance
remains false. D125 advisory scans remain historical; no new scan or actual Kiro model turn ran.
Independent review accepts `9ee8896` without an actionable finding. Candidate, strictly
resolved installed and retained binaries each pass 144 checks independently, with 18 component
checks, embedded metadata and all 105 production inputs verified. The current package graph
and dependency/notices records match. No production fix or refreeze is needed. Owner rights
stay outside this repository under D121.

## Media history artifact snapshot — D133, 2026-09-13

`third_party/inventory/macos-arm64-media-history.json` records the rebuilt 13,694,946-byte
development command, SHA-256
`569ede34b509ea259f41ae843f1874504960b0a9d72463c70866359301cc988b`. It identifies Go 1.27.1,
darwin/arm64, CGO_ENABLED=1 and clean revision `04eaad22d59fad672d31ba11bc65deccbf52bf0f` with
vcs.modified=false. D132 schema-dialects is the hashed predecessor and remains retained.

Only `internal/anthropic/media.go` and `internal/anthropic/request.go` change among the same
105 production input records. All 267 ordered import paths, selected files, four external
module versions/sums/package sets, native files, stdlib vendor packages and notices remain
unchanged. No dependency is added or upgraded.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod --snapshot media-history --binary dist/dax-kiro-proxy
```

Candidate and strictly resolved installed binaries each pass all 144 byte checks
(`d133-freeze.log`, `d133-installed-verify.log`); all 18 component checks pass
(`d133-components.log`). Install --force succeeds (`d133-install.log`). The first doctor
invocation verifies measured Kiro 2.21.3/Claude 2.1.269, login/policy and launch availability
(`d133-installed-doctor.json`). Client initialization remains unverified; release clearance
remains false. D125 advisory scans remain historical; no new scan or actual Kiro model turn ran.
Independent review accepts `418988d` plus the corrected README installed-artifact label.
Candidate, strictly resolved installed and retained binaries each pass 144 checks independently;
all 18 component checks, embedded metadata, 105 committed/current inputs and current ordered
package graph match. The post-document-fix verifier also passes. No production fix or refreeze
is needed. Owner rights stay outside this repository under D121.

## Terminal delivery artifact snapshot — D137, 2026-09-13

`third_party/inventory/macos-arm64-terminal-delivery.json` records the rebuilt 13,695,074-byte
development command, SHA-256
`3d386e521b29bd41bbded9f8255d789ebce8c91b887ec7a6b8174a2775d4cea7`. It identifies Go 1.27.1,
darwin/arm64, CGO_ENABLED=1 and clean revision `3a137754a70e49a40c1b32de00ccd78016500cd3` with
vcs.modified=false. D133 media-history is the hashed predecessor and remains retained.

Only `internal/session/driver.go` and `internal/session/turn.go` change among the same
105 production input records. All 267 ordered import paths, selected files, four external
module versions/sums/package sets, native files, stdlib vendor packages and notices remain
unchanged. No dependency is added or upgraded.

```sh
python3 tools/verify_dependency_inventory.py --gomodcache .cache/gomod \
  --snapshot terminal-delivery --binary dist/dax-kiro-proxy
```

Candidate and strictly resolved installed binaries each pass all 144 byte checks
(`d137-freeze.log`, `d137-candidate-verify.log`, `d137-installed-verify.log`); all 18 component
checks pass (`d137-components.log`). Install --force succeeds (`d137-install.log`). The first
installed doctor invocation verifies measured Kiro 2.21.3/Claude 2.1.269, login/policy and launch
availability (`d137-installed-doctor.json`). Client initialization remains unverified; release
clearance remains false. D125 advisory scans remain historical; no new scan or actual Kiro
model turn ran. Owner rights remain outside this repository under D121.

Independent review accepts `4f767aa` without actionable findings. Candidate and strictly
resolved installed binaries each pass 144 checks; all 18 component checks pass. Independent
comparison confirms the clean binary metadata, all 105 committed inputs, exactly two production
source changes and the current ordered 267-package graph. No correction or refreeze is needed
(`d137-review-report.log`, `d137-review-artifact-metadata.log`).
