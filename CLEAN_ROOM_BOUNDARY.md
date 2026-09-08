# Reimplementation and licensing boundary

## Purpose

These specifications were prepared by analyzing a user-authored ACP proxy implementation inside a
larger dual-licensed project. Source-level inspection was permitted for the specification author. No
source code, code excerpts, fixtures, comments, or repository paths are included here.

The next implementation phase uses a one-way boundary:

1. The specification author extracts behavior, protocol facts, invariants, and acceptance criteria.
2. This repository receives only those written specifications.
3. The implementation author works from this repository and public documentation only.
4. Conformance is measured with black-box tests, not source comparison.

This is a practical provenance control, not a guarantee that a court would find the result
non-derivative.

## Why the distinction matters

Copyright generally protects original expression rather than ideas, procedures, processes, systems,
or methods of operation. A protocol-compatible implementation can therefore be written from factual
behavioral requirements, but copying expressive code, comments, tests, or detailed structure creates
different risk.

The copyright holder of particular code can generally offer that code under multiple non-exclusive
licenses. That does not grant the holder authority to relicense code owned by other contributors.
Git authorship is useful provenance evidence but does not by itself establish legal ownership,
employment assignment, or work-for-hire status.

Authoritative background:

- U.S. Copyright Office, “What is Copyright?”: https://www.copyright.gov/what-is-copyright/
- U.S. Copyright Office FAQ on protected ideas and methods: https://www.copyright.gov/help/faq/faq-protect.html
- U.S. Copyright Office Circular 33: https://www.copyright.gov/circs/circ33.pdf
- GNU GPL FAQ on releasing code under more than one license: https://www.gnu.org/licenses/gpl-faq.en.html

## Repository rules

The repository must not contain:

- a Git remote, submodule, path, symlink, or build reference to the previous project;
- copied or mechanically translated code;
- copied tests, fixtures, prompts, comments, documentation, logs, or captured proprietary payloads;
- dependencies selected merely to reproduce the old implementation’s internal design;
- a claim that the clean boundary eliminates all AGPL or copyright risk.

Protocol names, standardized field names, endpoint shapes, observable event ordering, error status
codes, interoperability constraints, and independently measured behavior are permitted facts.

## Pre-implementation rights checklist

The repository owner must confirm and record all of the following before selecting a proprietary or
permissive license:

- ownership of the ACP proxy contribution, including whether it was created within employment duties;
- whether any co-authored or upstream expression is present in the behavioral specification;
- whether the organization has an invention, copyright assignment, or open-source policy that applies;
- permission to use Kiro CLI and its private extension methods in the intended environment;
- licenses of every implementation and test dependency;
- whether distribution is internal only or external.

If ownership or employer rights are unclear, obtain legal review. Do not assume that sole Git
authorship is sufficient.

## License decision

No software license is selected at the specification stage. The implementation repository should add
a license only after the checklist is resolved. If the implementation uses only newly written code
and license-compatible dependencies, the earlier project’s AGPL license is not copied into this
repository merely because the new program implements the same functional ideas. That conclusion is
still subject to the ownership and derivative-work review above.

## Audit trail

Before implementation starts, record a commit containing only these specifications. During
implementation, retain dependency lock files, license reports, public documentation links, design
decisions, and black-box test evidence. Do not retain the analysis clone or local path to the previous
repository as part of the project.

On 2026-09-09 the user supplied a review describing the earlier implementation and citing its source
locations. That description was visible in the conversation; the cited repository was not opened or
searched. This exposure is recorded explicitly. The user's delivery priorities are treated as product
direction and checked against this repository's requirements and public documentation. No cited
source, test, fixture, identifier or prose is imported. New implementation and tests still require
independent design and evidence; this note does not establish legal clearance.
