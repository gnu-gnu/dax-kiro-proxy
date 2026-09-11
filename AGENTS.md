# Agent instructions

## Mandatory reading

Before planning or modifying implementation code, read every Markdown document in the repository in
the order listed by `README.md`. Do not rely on a summary in place of the full documents.

## Source boundary

This is a specification-led reimplementation. The implementation agent must not open, search, clone,
import, copy, or compare against the previous implementation or its repository. Do not request its
path or remote URL. Do not use its package structure, identifiers, comments, tests, fixtures, logs, or
commits as implementation input.

Permitted inputs are:

- the specifications committed in this repository;
- public ACP, Anthropic API, MCP, JSON-RPC, JSON Schema, HTTP, and SSE documentation;
- behavior observed by running unmodified public clients and Kiro CLI as black boxes;
- dependencies whose licenses have been reviewed and recorded.

If the specification is ambiguous, add a decision record or an interoperability test. Do not resolve
the ambiguity by consulting the previous implementation.

## No copied expression

Write new source, tests, names, comments, and fixtures from first principles. Protocol field names and
other facts required for interoperability may be used. Do not reproduce prose or code from the
earlier project.

## Scope discipline

Build only the standalone Kiro ACP proxy and its launcher-facing contract. Do not reproduce unrelated
network interception, provider routing, request shaping, authentication refresh, MCP library, or
traffic-inspection features.

The proxy must not execute client tools. Tool effects remain under the client’s permission and hook
system. Kiro is launched with an execution-restricted agent and a session-scoped relay that can only
request tool execution from the client.

## Development rules

- Start with protocol fixtures and state-machine tests before integrating real processes.
- Keep public protocol support separate from Kiro-specific private extensions.
- Treat every process, socket, request, event queue, and pending tool call as bounded.
- Bind locally by default and authenticate all model-facing HTTP routes.
- Keep UI/status credentials incapable of authorizing model requests.
- Never log credentials, full prompts by default, tool outputs, or unrestricted subprocess stderr.
- Preserve cancellation and cleanup semantics under repeated cancellation.
- Do not silently fall back to a provider outside Kiro.
- Do not report estimated token counts as provider-billed token usage.

## Required gates

Before merging a feature, run the applicable tests from `ACCEPTANCE_SPEC.md`. Before the first
release, complete the dependency review recorded in `DEPENDENCY_REVIEW.md`; the owner's rights
checklist in `CLEAN_ROOM_BOUNDARY.md` is handled outside the repository (D121). Select a language
using `LANGUAGE_DECISION.md`, and record the decision.
