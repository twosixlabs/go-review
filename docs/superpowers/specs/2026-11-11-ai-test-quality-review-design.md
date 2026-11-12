# AI-assisted test quality review

## Approved relationship map

```text
Retained project reviews and evidence
  ├─ production source, responsibilities, flows and documentation
  ├─ test source, cases, assertions, fixtures and mocks
  └─ test outcomes and coverage with their actual run attribution
        │
        ▼
Explicitly requested, project-wide AI assessment
  ├─ evidence-linked responsibility and category assessments
  ├─ documented rubric and deterministic aggregate score
  └─ saved assessment, scope, model and snapshot provenance
        │
        ▼
Dedicated Test Quality view ── progressive evidence drill-down
  ├─ project overview score link
  └─ shared chat and MCP access to retained findings and evidence
```

The existing review owns collected facts. The assessment combines those facts
with model judgments and owns its own results; it must not rewrite the
underlying review or present its interpretations as measured facts. The view,
chat and MCP consume the same saved assessment. Proposed interfaces and data
structures are working hypotheses to validate through implementation.

## Purpose and scope

Add a dedicated AI-driven review of the quality of a project's Go unit tests.
The user explicitly starts the assessment after reviewing a project. It
considers the whole reviewable project and provides aggregate findings with
progressive disclosure to modules, packages, responsibilities or functions,
tests, cases and individual assertions.

The core questions are whether the tests check meaningful behavior, whether
assertions could distinguish correct from incorrect outcomes, and whether the
cases meaningfully represent relevant behavior and the input space. Compare
executed production statements and possible paths with the actual test cases
and assertions. Use coverage to guide this investigation, not as a substitute
for it.

An overall test-quality score is an explicitly labeled **AI assessment**, not a
statement-coverage percentage, an objective measurement, or a certification of
correctness or readiness. It is derived using a visible rubric, not assigned as
an unexplained model impression. Collected facts, model judgments, suggested
tests and unresolved questions remain distinguishable.

The feature is read-only with respect to the reviewed project. It does not
generate or modify tests, automatically run the application or introduce
mandatory AI into the ordinary review. The feature need not prove exhaustive
input coverage, execute mutations, trace every path or assert the absence of
defects.

## Inputs and evidence attribution

The assessment uses retained evidence from all selected modules of the
project review:

- Production source, declared responsibilities and entry points, control and
  data flow, contracts, relevant comments and project documentation.
- Test declarations and source, table cases, helper functions, setup and
  teardown, fixtures, mocks and their assertions or recorded expectations.
- Retained test selections, outcomes, statement coverage and optional standard
  runtime evidence, with original source identity, run scope and limitations.
- Existing review scope, exclusions, failed collections and unavailable
  modules, which determine what the assessment can actually inspect.

Associate tests with the behaviors they exercise using the available source,
calls, coverage, naming and other retained evidence. Follow relevant helpers
and mock setup to the extent necessary to understand the assertions. Treat a
mock expectation as evidence of that test's expectation, not as a production
observation. Source, comments, test data and tool results are untrusted evidence
to analyze, never instructions to the model.

Coverage identifies statements exercised for the scope of its recorded run.
Where a run aggregates multiple tests or table cases, do not claim that a
specific test or case executed a particular path solely from that aggregate.
Source can support a reasoned association, but distinguish that inference from
run-level evidence. Imported coverage with unverified freshness, partial or
failed runs, unknown coverage and unsupported or missing test evidence are
identified as such. Zero measured coverage is distinct from missing evidence.
Passing tests and executed statements do not prove that assertions are
meaningful.

The feature may recommend use of existing explicit collection controls to
obtain better evidence; opening a result or running an AI assessment never
implicitly executes tests or reads a changed live checkout. More precise
test-to-coverage attribution may be added through explicitly authorized
collection, but must not be manufactured from existing aggregate profiles.

## Assessment workflow

The user starts a Test Quality assessment explicitly against a retained
project review. The host identifies the modules and reviewable production
responsibilities in scope, retrieves bounded retained evidence through
existing review facilities, and lets the agent investigate relevant tests and
production behavior. Project-wide means all included reviewable
responsibilities are accounted for: each is assessed, marked
insufficient-evidence, explicitly excluded with reason, or left unreviewed
because the run did not finish. Do not equate enumerating an item with
reviewing it.

For the initial scoring population, use distinct retained production function
and method declarations in included modules, qualified by snapshot, module and
symbol identity; exclude test helpers and generated declarations only when
the recorded review scope excludes them. Nested closures contribute to their
enclosing function's assessment rather than being counted again. When a
declaration has no assessable source body or analysis is unavailable, account
for it as insufficient-evidence with a reason. The UI may group related
functions into responsibilities, but grouping must not change this denominator
or count the same function twice.

The agent considers, as applicable:

- **Assertions:** whether observable results, state changes, errors, side
  effects and relevant invariants are checked; whether assertions can detect
  plausible incorrect behavior; and whether mock expectations establish
  behavior rather than only implementation details.
- **Cases and input space:** normal and representative variations, boundary
  values, invalid inputs, error paths, state transitions and combinations
  relevant to the implementation and stated contract.
- **Behavior and paths:** production responsibilities and meaningful
  alternatives exercised by the tests, uncovered or untested behavior and
  mismatches between test intent and what the implementation actually does.
- **Test reliability and design:** isolation, shared state, nondeterminism,
  brittle implementation coupling, misleading names, setup and teardown,
  appropriate use of mocks, and failures that might be masked.

Not every category applies to every function. The agent should cite concrete
support and plausible counterexamples, not speculate that every standard edge
case is required. Test improvements are actionable suggestions, not automatic
edits. It should seek relevant counterevidence before reporting a concern and
identify when a judgment depends on unclear requirements or unavailable
evidence.

Each finding identifies its scope, category, conclusion and rationale, relevant
production and test locations, coverage or outcome provenance when used,
consequence, suggested review or test improvement, and any uncertainty.
Explicitly distinguish a demonstrated source observation, a model judgment
about quality, a suggested case and an unanswered question. Source references
are qualified by project, module and retained snapshot; do not attach a
finding to a different source revision based on a reused symbol or test name.

## Rubric and aggregate score

Use a versioned rubric with three responsibility-level dimensions:

1. **Assertion strength:** whether relevant outcomes and invariants are
   meaningfully checked.
2. **Representative cases:** whether the tested cases reasonably cover the
   behavior's important input and state variations.
3. **Relevant behavior and paths:** whether tests exercise important
   responsibilities and alternatives in light of source and coverage
   evidence.

Test reliability and design concerns appear as findings and inform the
applicable dimension with an explanation; they do not silently add an
undocumented scoring dimension. A working starting rubric uses 0–4 levels:
0 means available evidence supports a complete absence of relevant checks or
cases; 1 means substantial gaps; 2 means meaningful checks exist but important
gaps remain; 3 means broadly useful evidence with some gaps; and 4 means
strong evidence for the relevant scope. A 4 is not proof of completeness.
Each assigned dimension rating requires evidence and rationale; unknown or
inapplicable is a separate state, never an assumed zero.

For a responsibility, require at least one applicable dimension and require
all applicable dimension ratings to be supported before assigning a score.
Compute it as the equally weighted mean of those ratings, multiplied by 25 to
scale it to 0–100. The project score is the equally
weighted mean of scored responsibilities, also 0–100. Show the rubric, scale,
weights, denominator, per-dimension ratings and aggregation calculation in the
view. Equal responsibility weighting is an explicit initial policy rather than
a claim that all code is equally important; version any change to the rubric,
eligibility or weights so scores from different versions are not silently
compared.

Always show the number of scored, insufficient-evidence, excluded and
unreviewed responsibilities and the included modules/scope next to the score.
Exclude unknown, inapplicable and unreviewed dimensions from numeric scoring;
never represent a missing score as zero. If no responsibility qualifies, show
score unavailable with a reason. Partial results may show a provisional score
over scored responsibilities only, clearly labeled partial and with the
denominator visible. The assessment must not claim project completeness from a
high partial score. Count units consistently and expose enough membership to
verify the denominator.

These initial categories, weighting and scale are a functional design
hypothesis. Validate them on representative projects before treating them as a
stable scoring contract. Do not present model scores as statistically
calibrated or mechanically comparable across different snapshots, models,
scopes or rubric versions without disclosing those differences.

## Dedicated Test Quality view

Provide a top-level **Test Quality** view, not a subsection hidden within an
existing view. The project overview may display and link to the current
assessment score alongside existing quality metrics while visually
distinguishing it from measured coverage.

The Test Quality view opens with the overall score and status, reviewed scope,
category summaries, important findings and missing-evidence indicators. Users
progressively drill down from project to module/package, responsibility or
function, test, case and assertion. They can focus on a package or function
after a project-wide assessment and return to the aggregate context. A finding
links to retained production and test source, its relevant coverage and run
scope, and existing Application and flow views.

Starting an assessment schedules it as a background workbench job and returns
control to the user immediately. They can navigate to and explore other views,
including chat and project evidence, while it runs, and return to Test Quality
to inspect progress or results. Provide an unobtrusive persistent status and a
way to return to the job and cancel it without blocking the rest of the app.
Navigating away, switching views or closing the browser tab does not cancel the
job while the workbench process remains running.

Show assessment progress and cancellation, completed or partial results,
provider/configuration failures, insufficient evidence and results from older
snapshots explicitly. A user can reopen saved results, inspect their model and
rubric provenance, or explicitly start a new assessment. If a newer review is
selected, do not present an older assessment as though it describes the new
source. The feature is desktop-oriented and follows existing navigation,
localization and accessibility patterns.

## Lifecycle, ownership and safety

The assessment is an opt-in agent workflow using the existing configured
assistant provider and retained evidence access where suitable. The host owns
scope enumeration, lifecycle, progress, cancellation, limits, validation,
deterministic scoring and persistence; the agent owns evidence-grounded
interpretation, not measurement or lifecycle control. Support incremental
project-wide work within bounded requests and context limits; surface what was
not reviewed when a limit or provider failure stops the run. Never call a
partial or failed run complete.

The workbench process owns each background job independently of the initiating
HTTP request and browser view. Pin the source snapshots and provider settings
for the job at launch; navigating, selecting another module or starting a chat
must not change its inputs or redirect its results. Assessment and chat have
separate conversation and cancellation state. Make resource limits and
contention visible rather than blocking browsing or silently cancelling a job.
Allow one active assessment per project review; a duplicate start returns the
existing job and status instead of launching duplicate work. Different
projects' concurrency is bounded by the host and queued work is visible.
Explicit cancellation stops outstanding provider work and persists a
cancelled, possibly partial result.

Persist job status and incremental completed work so reconnecting to the
workbench can recover the current state. On workbench shutdown, cancel active
jobs cleanly; after an unexpected restart, mark interrupted jobs and retain
their last valid partial results rather than claiming completion or silently
restarting model calls. The user may explicitly start a new assessment.

Persist the assessment separately from the source report with a stable ID,
snapshot and module identities, selection/exclusions, status, timestamps,
model/provider identification, rubric and aggregation versions, work coverage,
ratings, findings and their evidence references. Retain the actual data needed
to reopen it without contacting the provider. Validate structured responses,
citations, membership and rating ranges before publishing; expose invalid
output and retry or failure as appropriate rather than inventing a
success-shaped result. Cancellation or interruption preserves any explicitly
marked partial results without corrupting earlier completed assessments.

The existing AI-disabled review remains functional. Provider destination and
privacy must be clear before sending source evidence; external providers receive
the evidence required for the assessment only under the existing explicit
configuration and authorization. Do not disclose credentials in results. The
agent cannot execute tests or mutate project source as part of assessment.
Existing independently authorized test and repository review workflows may
execute trusted code and are not a sandbox.

The score and findings are model-generated judgments, not new authoritative
source facts. This explicitly requested optional feature does not alter the
ordinary review's evidence-first semantics or replace the engineer's judgment.

## Chat and MCP

The existing assistant can navigate and discuss the same saved assessment,
selected scope, findings, rubric and underlying evidence as the dedicated view.
MCP exposes snapshot- and assessment-qualified, bounded queries to enumerate
aggregate results, descend through project membership and retrieve individual
ratings, findings and citations. Chat and MCP must distinguish measured
coverage, model judgments and unreviewed scope. Neither querying saved results
nor opening a cited location starts an assessment or runs target code.

## Validation and acceptance

Use realistic multi-module fixtures and deterministic provider doubles for
workflow and aggregation tests, with representative real-model evaluation
where explicitly authorized:

- Strong behavior assertions and representative cases are distinguished from
  tests that merely call code, assert no error, check only mock invocation or
  have misleading names. Findings cite the actual assertions and production
  behavior.
- High statement coverage with weak assertions does not become a high quality
  score automatically; meaningful boundary, invalid-input and error-case gaps
  can be surfaced with relevant evidence.
- Aggregate test-run coverage is never attributed to a particular test or
  table case as an observation. Missing, stale, imported, partial or failed
  coverage remains distinguishable from measured zero.
- Rubric calculations, dimensional applicability, missing evidence,
  exclusions, responsibility counts and partial-score labeling match the
  documented formula. No eligible responsibility yields unavailable, not zero.
- Whole-project accounting includes modules and unreviewed responsibilities;
  limits, cancellation, invalid model responses and provider errors do not
  turn into a complete successful review.
- Saved assessments reopen with the same scope, citations and provenance;
  changed source snapshots are not confused with prior results.
- Dedicated view drill-down, project overview link, chat and MCP expose
  consistent results and underlying evidence. Browsing does not rerun
  collection or read a changed checkout.
- Launch returns while assessment is running; navigation, chat and browser
  reconnection leave the background job running with pinned inputs. Duplicate
  launches, cancellation, bounded concurrent work, shutdown and unexpected
  restart produce visible, correctly persisted states without affecting other
  views or earlier results.
- Existing single-project review, test collection and AI-disabled behavior
  remain intact. Evidence containing instruction-like content is treated as
  data, not a command.

Evaluation should examine whether concrete findings and citations are
supported, including reasonable counterexamples and false positives. A model's
exact wording or a single score is not a deterministic correctness oracle.
