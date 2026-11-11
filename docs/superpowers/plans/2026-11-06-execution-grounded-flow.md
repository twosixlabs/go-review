# Architectural System Flow Implementation Plan

The exhaustive instrumentation plan has been replaced, not retained as a second
implementation track. The approved scope is
[architectural production flow](../specs/2026-11-06-execution-grounded-flow-design.md).

## Execution order

1. Derive source-linked components and broad information exchanges from
   production ownership, contracts and wiring.
2. Infer relevant executed alternatives from standard coverage and CFG
   witnesses; preserve genuine uncertainty.
3. Replace custom recording in existing review/API collection with explicit
   selected-unit coverage and optional standard trace. Remove exhaustive
   recorder/helper/snapshot machinery and active recorder controls.
4. Make context/component/operation architecture the application view with
   source, contract and coverage evidence drill-down.
5. Demonstrate and integrate useful standard Go runtime observations without
   requiring a complete function-call timeline.
6. Validate Builder server across its behavior using explicit safe unit-test
   selections, evaluate WASM secondarily, and remove obsolete requirements,
   tests, UI and documentation.

Use sequential implementation and inline review because source identity,
coverage provenance and navigation are shared. Each unit starts with a failing
behavior test and uses the named functional verification script. The session
implementation plan carries detailed steps and acceptance criteria; no task in
the removed exhaustive plan remains active.
