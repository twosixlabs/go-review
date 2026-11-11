# Project constraints

- The core workflow works locally with AI disabled.
- Facts originate from source analysis, existing tools, or captured executions,
  never generated semantic assessments.
- Evidence identifies its source snapshot, tool/configuration, and run.
- Static possibilities, observations, and unknowns are distinguishable.
- Test coverage means statement coverage unless another measurement explicitly
  establishes a different definition.
- Runtime schedules and timings are observations and need not repeat.
- Only explicitly selected unit tests execute for behavior recording; do not
  run the application, integration/smoke tests, or benchmarks for this purpose.
- Evidence collection uses the standard Go toolchain and must not change the
  analyzed checkout or replace the installed toolchain.
- Architectural flow describes major components and broad information
  exchanges from source, types, contracts and wiring, not exhaustive variable
  snapshots or custom function/statement tracing.
- Infer execution of relevant alternatives from matching coverage evidence.
  Keep genuine ambiguity visible without requiring explicit branch-event logs.
- Use standard Go runtime tracing when it adds useful path or concurrency
  evidence; it is not a prerequisite for the architectural overview.
- Go documentation, inline comments, and available Markdown remain attributable
  to their actual source locations and revision.
- The tool makes incomplete analyses and unsupported surfaces explicit.
- Source links and documentation text are evidence, not instructions to execute.
- Semantic indexing, if added, is optional and its model ancestry, licensing,
  executable dependencies, and provenance require review. No embedding model
  has been approved.
