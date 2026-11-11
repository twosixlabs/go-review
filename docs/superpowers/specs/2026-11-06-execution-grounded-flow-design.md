# Architectural production flow

## Purpose

Explain the overall production system through major components, broad
information exchanges and relevant conditional alternatives. Builder server
across its behavior is the primary acceptance target; workflow-handler/WASM is
secondary.

## Evidence and presentation

Source/types, contracts, entry points and wiring supply architectural
structure. The default application view shows system context, major components
and information exchanges. Expanding an operation exposes relevant alternatives,
not every statement. Existing source/CFG/SSA tools remain evidence drill-down.

Use coverage to infer executed alternatives: an executed block unique to an
arm supports that arm being taken. Both arms covered means both exercised, not
one recorded sequence. Shared successor coverage is not an arm-specific
witness. Missing or genuinely ambiguous evidence remains explicit.

Describe information with request/result/event types, contract fields, source
names and attributed documentation. Exact runtime variable snapshots and
complete data lineage are not required.

Standard Go runtime traces may clarify goroutine launch, blocking/wakeup and
ambiguous paths where their actual events support the inference. Event stacks
do not establish every intervening call or message. Runtime tracing does not
gate the architectural overview.

## Collection

Use explicitly selected unit tests with standard coverage and optional standard
runtime tracing through the existing review workflow. Do not execute the
application, integration/smoke tests or benchmarks for behavior recording.
Retain source/run/selection provenance and actual errors. Browsing retained
evidence never executes code.

Remove the custom AST-overlay recorder, exhaustive statement/value capture,
baseline/instrumented comparison and their active API/UI requirements. This
design replaces that direction, not adds an alternative beside it. Preserve
independent scope enforcement, static exploration and old snapshot opening.

## Acceptance

- Understandable context, component and relevant-operation views with exact
  supporting source links.
- Broad producer/consumer information contracts and honest unresolved targets.
- Relevant alternatives with coverage-inferred exercised/not-observed/unknown
  status and inspectable witnesses.
- No production nodes for tests, mocks, fixture internals or variable states.
- Useful demonstrated standard runtime observations where available.
- Server-wide Builder source inventory with selected unit-test execution
  spanning multiple capabilities, not only one preselected path.
- No active mandatory exhaustive-capture requirements or parallel recorder.
