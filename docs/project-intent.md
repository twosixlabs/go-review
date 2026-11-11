# Project intent

Go Review is a local, Go-first visual review workbench for human engineers.
Builder is the first assessment target, not a dependency of the tool.

The reviewer is an experienced Go engineer. Optimize for understanding the
application, not teaching Go. Keep labels concise and distinguish analysis
limitations without explaining standard language semantics.

The purpose is to make codebase maintainability and readiness for
productionization easier to assess, particularly when reviewing AI-generated
code. Whole-codebase review comes first; revision and PR/MR review are a later
workflow using the same evidence.

The primary workflow is one command or one Review repository click. Be opinionated
about evidence collection: discover the repository and its modules, execute the
standard checks and conventional repository verification entry point, retain the
results, then let the engineer review them. Do not require per-tool configuration,
separate scripts, or manual evidence assembly for the normal review. Execution is
explicit; browsing retained results never implicitly reruns repository code.

Engineers should be able to explore structure, possible call paths, observed
test executions, documentation, coverage, and complexity without reading whole
packages to reconstruct the relationships. Source inspection is targeted
drill-down, not the primary interface.

The primary behavior view explains the overall production system: major
components, broad information exchanges and relevant branch alternatives.
Source/type/wiring analysis supplies the architecture; coverage supports
inference about which alternatives selected unit tests exercised. Standard Go
runtime evidence supplements that explanation when useful for concurrency or
ambiguous paths. Exhaustive variable capture and a call-by-call execution
timeline are not requirements.

Start with recognizable application surfaces, such as available HTTP endpoints,
not a randomly selected internal function. Present behavior in possible
execution order with branches and boundaries, then let the reviewer expand
implementation details without losing their place. Package organization and
analysis convenience must not obscure the application's logical behavior.

The tool collects and presents evidence. It does not certify correctness or
compute an authoritative trust score. An agent may navigate the evidence, but
its interpretation must not become an authoritative analysis result.

## Non-goals

- Replacing engineer review or asserting production readiness automatically.
- Reimplementing mature analysis capabilities when integration is practical.
- Requiring a model, embedding service, cloud service, or external upload.
- Treating executed statements as proof of meaningful assertions or complete
  use-case coverage.
- Treating a static call graph as a runtime sequence or as data lineage.
