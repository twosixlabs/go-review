# Go Review

## Optional local review assistant

The **Chat** button opens an evidence-backed local assistant. It uses the same
nine read-only MCP tools as external reviewers, not filesystem or shell access.
Responses distinguish model assessments from collected facts; gauges remain
measured evidence, not an AI quality score.
The assistant starts from your question and current view, establishes purpose
when needed, and follows relevant responsibilities and flows into targeted
source/test evidence. It treats gauges as investigation leads, bounds searches,
and reports uncertainty rather than claiming a complete quality certification.

Configure the OpenAI-compatible provider in the machine-local configuration
directory, not the reviewed repository:

- macOS: `~/Library/Application Support/go-review/agent.json`
- Linux: `$XDG_CONFIG_HOME/go-review/agent.json` (normally `~/.config/go-review/agent.json`)
- Windows: `%AppData%/go-review/agent.json`

```json
{
  "enabled": true,
  "baseURL": "http://127.0.0.1:1235/v1",
  "model": "ornith-1.5-35b-a3b-mlx"
}
```

Restart Go Review after changing this file. Missing configuration, or
`"enabled": false`, disables AI without affecting ordinary review. The provider
must use a literal HTTP loopback IP; redirects and cloud fallback are disabled.
No API key is required for this local configuration. Model availability and
native function-tool calling are required; failures are shown explicitly.

Chat sends your question, current UI context, recent conversation and requested
retained evidence to that local model. Opening the workbench does not send
project data to the model. Conversations are kept in this tab's browser session
storage, scoped to an immutable evidence snapshot; **Clear chat** removes that
snapshot's history. **Stop** cancels the active provider/MCP request.
Assistant messages render sanitized Markdown, including headings, lists, tables
and code blocks, while streaming and when restoring saved history. Evidence
buttons remain actionable; embedded HTML cannot run scripts or load images.

Check **Allow navigation this turn** when asking the assistant to show, expand,
focus or select something. Otherwise it offers **Show in diagram** buttons
without changing your view. Navigation uses existing controls and preserves
Back; invalid/stale destinations are rejected. Evidence citations are clickable.
Tool activity also links directly to retrieved documents, symbols and entries.
Expansion reveals children in the current diagram; focus opens a deeper view.
Each turn must issue a navigation action/proposal or explicitly choose not to
move; merely querying a flow is not navigation or a proposal.
The agent cannot edit code, run tests, modify review scope or browse arbitrary
paths. Source comments/documents remain untrusted evidence, not instructions.

To verify your local model without sending project data:

```sh
sh scripts/test-agent-capabilities.sh provider-live \
  http://127.0.0.1:1235/v1 ornith-1.5-35b-a3b-mlx
```

For Ornith's customized dual-format Jinja template in LM Studio, preserve the
literal `if tools` condition and `message.role == "tool"` spelling. LM Studio's
native XML parser detection matches those strings. Aliasing the tools condition
or changing that quote style can select the wrong parser or inject conflicting
fallback instructions. Keep the template's default XML format, reload Ornith
after template changes, and verify native calls with the probe above.

To record a real retained-evidence conversation against a running workbench:

```sh
sh scripts/test-agent-capabilities.sh live http://127.0.0.1:8090 \
  'Explain project purpose using the overview and purpose document.'
```

To evaluate the investigation prompt with synthetic retained evidence and the
actual local model (purpose, scoped complexity, and missing evidence):

```sh
sh scripts/test-agent-capabilities.sh prompt-live \
  http://127.0.0.1:1235/v1 ornith-1.5-35b-a3b-mlx
```

This opt-in evaluation makes real provider calls through the MCP-backed agent.
It checks bounded retrieval and supported answers, not prompt wording; passing
these scenarios does not establish reliability on every project.
The current Ornith evaluation passes scoped metric attribution and missing-symbol
handling, but still fails the orientation retrieval budget. It can also narrate
or repeat navigation bookkeeping despite instructions not to. These are known
model-following limitations, not guarantees enforced by the prompt.

Exploration is bounded to 16 model steps per turn, with explicit context/output
limits. Ask focused follow-ups for larger investigations. A local model may
misinterpret evidence or fail to call tools; neither successful tool calls nor
test coverage certify project quality.

A local interactive Go code exploration workbench for human engineers.
No AI, cloud service, or external web assets are required.

## Run

Requires Go 1.24 or newer and the Go version required by the target module.

One command runs the review and opens the results:

```sh
go run ./cmd/go-review review /absolute/path/to/repository
```

For Builder, use `/Users/russell.brasser/projects/builder`. An installed binary
uses `go-review review /path/to/repository`. Alternatively, start `go-review`,
enter a repository path, and click **Review repository**.

Review is opinionated; there is no per-check configuration:

- Find the containing Git repository even when given a nested module, then
  discover its Go modules. Hidden directories, dependency trees, build outputs
  and test fixtures are excluded. Outside Git, the supplied directory is the root.
- Collect application/call/control-flow structure, complexity, documentation,
  architecture signals, formatting, syntax and type-resolution checks.
- Execute the conventional `scripts/verify-repository.sh` once from the repository
  root only when separately authorized in Review scope, preserving its exit status and output. This can include
  frontend, contract and acceptance checks that generic Go tooling cannot infer.
  An absent entry point is explicitly Not run; arbitrary scripts are not guessed.
- Run `go vet` and bundled `golang.org/x/tools` rapid-type reachability analysis
  with executable/test roots. Run all in-scope Go tests with standard cross-package
  statement coverage when no unit selections are saved. Saved exact selections
  restrict execution to those tests, once per selection, also with coverage.
  Normal test execution includes examples and fuzz seed tests, not benchmarks
  or fuzz campaigns; path and test-name exclusions still apply.
  Reachability is current-build evidence, not proof that code is safe to remove;
  modules without executable/test roots remain unassessed.
- Keep collecting after check failures. Failed/unreviewable modules, missing
  prerequisites, analysis limitations, and execution errors stay visible.
  Coverage and traces are withdrawn if source consistency checks fail.
- Keep only the current result for each repository, with its sources, command
  diagnostics, coverage and optional standard trace artifacts outside the checkout
  in the OS user cache under `go-review/runs`. A completed replacement deletes
  superseded review evidence, including old selected-test runs. Refreshing one
  module carries forward only the other modules' still-current artifacts.
  **Analyze only** replaces execution evidence with a fresh static snapshot.
  The workbench shows progress, module selection, and **Review results**.
  Running reviews have a persistent progress panel with the current stage,
  module, elapsed/stage time, and time since server confirmation. Reopening a
  page reconnects to an active review, and already-open pages detect new runs.
  External commands show their executable, arguments and working directory;
  in-process analysis continues to show its stage. Connection failures remain visible while
  polling retries. After module discovery, the bar shows completed/total stages
  and percentage, not estimated time remaining. Failed and explicitly skipped
  stages count as processed, not passed; diagnostics retain their outcomes.
  During discovery the stage total is unknown and the bar is indeterminate.
  Completion or cancellation loads the current result. The summary distinguishes
  failed checks, unavailable modules, and checks not run; skipped checks do not
  mark a review failed. **Review results** explains individual outcomes.
  Reopen a current result with `go-review open /path/to/run/review.json` without
  executing it again. Old run directories are not an archive and are removed
  when replaced.

### Review scope

Open **Review scope** near the repository controls to save exclusions for that
repository. **Load saved scope** reads the rules for the entered directory;
**Save scope** changes only future reviews/analyses. The Review button and the
single CLI command automatically reuse the same saved rules. Existing retained
results keep the policy under which they were collected; **Applied scope &
exclusions** shows that policy and its exclusion inventory. Old snapshots
explicitly indicate that they predate this policy.

- Generated Go sources are automatically excluded from review findings, gauges,
  statement-coverage totals and normal graphs.
  Detection recognizes Go's generated-file header and wit-bindgen's header.
  Sources remain available for compilation, type resolution and handwritten SSA;
  calls into excluded code remain explicit boundaries rather than disappearing.
- Excluded paths are repository-relative files/directories or Go-style path
  globs, one per line; `**` matches any number of directory segments. Matching a
  directory includes its descendants. For example, exclude dedicated tooling
  with `server/cmd/cosmonic-bench-*`, or selected test files with
  `**/smoke_test.go`. Names alone are not automatically treated as non-project
  code: a smoke test can be a valuable correctness test.
- Excluded test names are Go regular expressions against top-level
  Test/Example/Fuzz/Benchmark names, such as `^TestPerformance`.
  Subtest selection is not supported: exclude the parent test or file.
  Selected unit tests, vet findings and test reachability roots
  use the same policy. Test entry points are renamed in isolated build overlays;
  explicitly excluded test files become empty packages. The checkout is unchanged.
  Required helpers, package initialization, TestMain and dependencies can still
  execute. Name-excluding TestMain, leaving no target packages, or breaking a
  required helper dependency produces a visible error, never a broader fallback.
- Save exact unit selections as `import-path::TestName,TestName`, one per line.
  Package wildcards, test-name expressions and benchmarks are not selections.
  Leave this field empty to run all in-scope tests with coverage during Review.
  **Analyze only** never executes tests, regardless of the saved selections.
- **Repository verification is separately authorized.** Its internal commands
  own their scope, even when Go Review has exclusions. It never runs implicitly
  to collect flow evidence. Missing authorization or an excluded script produces
  a visible Not run result.

Rules are stored locally under the OS user configuration directory at
`go-review/scopes/<repository-root-hash>.json`, not inside the target checkout.
Source snapshots include the applied rules in their identity. Raw command
diagnostics are retained even when findings outside the scope are filtered.
New selections replace previous aggregate coverage rather than attributing it
to the new tests. Failed selections retain partial aggregate evidence and actual
matched test outcomes; skipped or unmatched tests are not passing evidence.
Counters shared by several tests do not identify which test contributed a hit.
Failures and raw diagnostic artifacts from the current result remain visible.
Reopening it never reruns collection; previous snapshots are not kept.
Use `sh scripts/test-scope.sh` for the focused policy/API/UI fixtures, or add
`all` for frontend tests, the full Go race suite, vet and build.

**Review executes repository code.** The command or button is explicit execution
consent. Use only a trusted checkout; this is not a security sandbox. Tests and
repository scripts may have their own filesystem, network or service side effects.
Go Review does not auto-format source or install system tools. Go commands use
read-only module flags and the local toolchain; dependency downloads and Go cache
writes may still occur. Commands have a 15-minute upper bound and reviews a
one-hour upper bound; Go's own test timeouts may be shorter. Cancellation kills
owned subprocess groups on Unix. Unsupported checks never become passes.

For static exploration without executing tests/scripts, use **Analyze only**
with a Go module path, or:

```sh
go run ./cmd/go-review -dir /absolute/path/to/go/module
```

Open http://127.0.0.1:8090 if a browser was not opened automatically.
Use `-addr 127.0.0.1:8091` to choose another port. The listener is restricted to
loopback (for a subcommand, put the flag before its repository/report path).
Stop with Ctrl-C. The module picker can analyze another module while
the application is running. Documentation defaults to the containing Git
repository, or the module when no repository is found. The optional Docs directory
picker overrides that root. The root and document path remain visible.
Analysis invokes `go env` and `go list` with
read-only module flags and the local toolchain; dependency downloads and Go
cache writes may still occur.

## Read-only MCP

### Entry-point discovery

New analysis retains one entry inventory for the visual overview, MCP and chat:

- **Wasm components:** exported functions/interfaces in the selected WIT world,
  using available component/wasmCloud configuration as selection and invocation
  evidence. Host imports are not incoming entry points. Verified generated
  export bindings link contracts to Go implementations; unresolved contracts
  remain visible with their reason instead of falling back to an empty `main`.
- **CLI programs:** `main` in each main package. Exported helpers in the same
  application module do not become library entry points.
- **Standalone library modules:** exported functions and methods on exported
  receiver types in public packages. Internal packages and tests are excluded;
  types, constants, variables and other package evidence remain browsable.
- **HTTP services:** supported HTTP registrations remain entry points, with
  program `main` available for startup exploration.

Go `init` declarations are separate supporting initialization, not external
entries. Each entry opens the same progressively disclosed implementation flow.
**Entry declaration & evidence** exposes the retained Go/WIT/configuration
provenance, including full contract/configuration text without executing it.
Generated ABI scaffolding remains excluded from review gauges.
Wasm input-decoding helpers remain visible as preparation stages. WIT `Result`
constructors are collapsed in the activity overview, but their complete return
expressions remain available in function steps and source. External dependency
handoffs remain explicit even for import paths without a domain name; their
implementations do not acquire local quality or coverage measurements.

The main-rooted unreachable-code check is **Not run** for Wasm components: it
does not model WIT export roots. Selected packages that depend on retained
`go:wasmimport` declarations also require a component-aware unit-test harness;
this is **Not run**, not a failed component test. Host-compatible component
logic without these dependencies can still use ordinary Go unit tests, even
when other packages in the module use Wasm imports.

Wasm discovery reads `componentize-go.toml` (`worlds`, `wit_paths`),
`wasmcloud.toml` (`wit_world`, `wit_path`), and available wasmCloud
Workload/Application YAML for world selection and trigger evidence. WIT support
includes versioned interfaces, direct/inline exports and plain world includes.
Resource methods, include renaming, ambiguous/missing definitions and unsafe or
out-of-bounds sources are reported as limitations, not invented implementations.

Older retained snapshots are marked as legacy: **Analyze only** is needed to
collect the new WIT/configuration and public-API evidence. Browsing a retained
snapshot never reads current target files or silently invents missing entries.

### MCP exploration

The same workbench serves a local Streamable HTTP MCP at
`http://127.0.0.1:8090/mcp` (use your configured workbench address).
Open a retained run normally; connecting to MCP does not collect evidence or
execute target code. No separate daemon, Node installation, AI service or
deployment platform is required.

For example, VS Code's MCP configuration can register:

```json
{
  "servers": {
    "go-review": {
      "type": "http",
      "url": "http://127.0.0.1:8090/mcp"
    }
  }
}
```

Start with `review_overview`, optionally selecting an exact retained `module`.
Its `purposeDocument` identifies the same project-documentation entry as the UI;
read it with `review_document` rather than treating a missing package doc as a
missing project purpose.
Use the returned `snapshot.id` as `snapshotID` in subsequent calls. This digest
identifies the complete retained report, including coverage and test evidence,
not just its source revision. Module selection through MCP never changes the UI.
Replaced evidence produces a stale-snapshot error rather than silently changing
the meaning of an existing node ID.
`projectKind` identifies the entry strategy. Use the `entries` section for all
entries, `initialization` for supporting initialization, `entryEvidence` with
`entryID` for a declaration's provenance, and `entrySources` for retained WIT
and configuration document IDs. Read those IDs with `review_document`.

- `review_flow`: entry-point overview, in-place `expandedIDs`, or
  `focusSymbolID`. For deeper focus, carry previous focus IDs in `focusPath`.
- `review_inspect`: compact visible node/edge summaries; explicit sections reveal
  comments, documentation, represented members, children and source alternatives.
- `review_architecture`: system context, components and information exchanges,
  using the same projection as the UI.
- `review_packages`, `review_search`, `review_document`: structure and complete
  retained documentation.
- `review_source`: bounded original source/test lines by retained file ID,
  never an arbitrary filesystem path.
- `review_evidence`: retained diagnostics, scope, tests, coverage provenance and
  targeted function steps/calls/references. Optional `runIndex` reveals retained
  standard runtime observations; it never creates them. Overview/run inventories
  contain diagnostic metadata, not full command logs. Request `checkID` for
  diagnostic text, or `commandIndex` with `section: "stdout"` / `"stderr"` for
  retained output. Test-run inventories
  contain counts; `runIndex` reveals paginated matched tests and outcomes rather
  than repeating every test name in every function inspection.

Discovery does not inline full membership, file/import inventories, CFGs,
architecture component catalogs, comments or command output. Responses list
available `sections`; select one with `section` when that detail is needed.
For example, `review_packages` with `package` and `section: "symbols"` pages
definitions; `review_evidence` with `symbolID` and `section: "controlFlowNodes"`
pages implementation steps. `review_flow` and `review_architecture` return
`nodePage` / `edgePage`; their `nodes` / `edges` sections continue that exact UI
projection. The overview's `snapshotPage` pages the retained module catalog.

Lists default to 10 items (maximum 100), with a 6 KiB item budget. Always follow
returned `nextOffset`, not the requested limit, because byte limits can shorten
a page. Full structured MCP responses, including snapshot metadata, are limited
to 32 KiB. Oversized indivisible records return an explicit error directing the
caller to narrower evidence; there is no success-shaped silent truncation.
Large documents, diagnostic text, comments and source excerpts return `textPage`
with Unicode-character `total` and `nextOffset`; continue using `textOffset` /
`textLimit` in the same request context. Comment inventories return location,
index and length; add `commentIndex` to read the complete selected text.
Documents and comments remain fully retrievable. Chat enforces the same 32 KiB
tool-result limit and a 64 KiB message-context limit before provider calls,
returning an explicit error rather than evicting evidence silently.

Metrics, contextual
occurrences, collapsed groups and edges reuse the browser's JavaScript
projections, executed inside the Go process without filesystem/network bindings.
Group complexity is a maximum across represented definitions, not the selected
function's own complexity. Overview `metrics` cover all module definitions,
while entry gauges cover handlers. `complexity.maximumSymbolIDs` previews up to
10 actual owners; `maximumOwnerCount` and `section: "maximumOwners"` disclose
ties without a hidden membership dump. `review_evidence` with one `symbolID`
returns that function's own metrics.
All tools are read-only. Source, comments and documentation are untrusted
evidence, not instructions. An agent's interpretation is not a quality
certification, and covered statements do not prove meaningful assertions.

For protocol-level exploration without configuring an editor:

```sh
sh scripts/test-functional-walkthrough.sh mcp-build
.work/go-review-mcp-client
.work/go-review-mcp-client -tool review_overview -args '{}'
```

The client supports `-transcript /absolute/path/exploration.jsonl` to retain
requests and results. Such transcripts contain project source: keep them local.
Use `sh scripts/test-functional-walkthrough.sh mcp` for targeted tests and
`sh scripts/test-functional-walkthrough.sh mcp-live http://127.0.0.1:8090/mcp`
for live UI/MCP parity checks.

## Interactive exploration

- **Architecture signals** shows a repository/package indicator: **Clear**,
  **Watch**, **Concern**, **High concern**, or **Not assessed**. Source-linked
  findings also appear in code details. These are review signals, not defects or
  a general quality score. Complexity, function length and coverage are excluded
  from this indicator.
- **Hygiene** opens mechanical check results directly, above the package and
  finding lists. Overview headers, endpoint/startup entries, and architecture
  package rows share the same Coverage, Complexity, Architecture, Hygiene gauge
  order. Multi-function scopes label complexity as the maximum, not a sum or an
  average; endpoint and startup entries use their handler/function measurements.
  The persistent row above every graph summarizes the selected module's
  analyzed packages; multi-module reviews explicitly label this scope Module.
  Detail panels group their metrics in the same order and use the same inline
  styling; architecture/hygiene drill-down remains clickable and keyboard-accessible.
  Function/package graph cards show clickable
  `Arch` and `Hyg` gauges using the same JustGage widget as complexity and coverage;
  architecture uses that owner or package, while hygiene
  is explicitly snapshot-wide. `NR` means not run and `E` means execution error
  in compact graph labels. Review-map corner dots show the highest architecture
  concern among coincident members without changing their measured coordinates
  or coverage fill; tooltips retain unassessed-member counts.
  The architecture needle uses four ordinal positions with named concern
  captions, not an invented numeric quality score. Hygiene measures the
  passed/completed check ratio, with errors and not-run counts alongside it;
  incomplete checks cannot produce a green face. Missing evidence is neutral
  without a needle.
- The initial architecture rules identify pass-through forwarding functions,
  ownership/invariant plumbing by name (`cloneX`, `copyX`, `WithXKey`, `ErrXNil`),
  and interfaces with at least 10 explicitly declared methods (20 is high).
  Forwarding and ownership matches are low-severity prompts to inspect whether
  the boundary is justified; they are not automatic recommendations to delete it.
  Known type conversions and builtins are not forwarding layers.
- A medium finding gives **Concern**, a high finding **High concern**. Three
  distinct affected owners covering at least 20% of assessed function/interface
  declarations escalate low findings to **Concern**, or medium findings to
  **High concern**. Repeated matches of one rule on one owner are deduplicated;
  multiple rules on one owner do not increase recurrence. The repository uses
  the highest package concern, with unassessed package counts kept visible.
- **Mechanical hygiene** stays separate: formatting, parsing and existing type
  resolution are checked against collected production Go source. The UI shows
  passed/completed checks, errors and not-run checks separately. Repository
  authorized verification, vet, reachability analysis and selected unit tests are collected
  by **Review repository**. In static-only exploration they remain **Not run**;
  results are never inferred from an imported profile or another check.
  Generated files, test files, inactive build tags and dependencies are excluded
  from source-rule assessment; explicitly executed tests and repository scripts
  have the scopes recorded in their check details.
  Parse errors and scopes without supported declarations cannot be
  marked Clear. Assessment rules are versioned and bound to the snapshot source
  hash; older or mismatched snapshots display Not assessed.
- **Application** starts with project-specific entries: Wasm exports, CLI `main`,
  standalone library public functions/methods, or HTTP registrations.
  **Startup & wiring** keeps supporting `init` declarations separate.
  **System context** opens the system boundary,
  actors and grouped dependency contracts with incoming/outgoing information.
  **Components & exchanges** groups implementation by source package and receiver;
  select a group, then a component and operation to inspect meaningful alternatives.
  The explorer lists **Group components** and **Component operations** directly;
  selecting a component does not require finding its card in a large diagram.
  Supporting implementation stays behind the component. Grouping is inferred
  structure, not a certified business-role classification.
- Request/input and response/output contracts have distinct directions.
  Labels describe source types and names, not runtime values or object lineage.
  Arrows are information relationships, not one observed chronological scenario.
  Tests and test-only support imports are evidence, not production components.
- Alternatives show **exercised**, **not-observed** or **unknown**, with reasons,
  enclosing prerequisites, source links and coverage witnesses. Mixed summaries
  preserve their children. Unobserved static alternatives remain available.
  Exact selection/outcome, source identity and optional runtime evidence are
  available in the evidence panel. Browsing never executes code.
- **HTTP endpoints & startup** returns from architectural views to the entry-point
  inventory. Choosing an endpoint or startup entry opens **Overall flow** first:
  a connected diagram of source-owned responsibilities. Solid arrows are possible
  source continuations; dashed arrows launch background work, with no inferred
  completion before the response. The longest forward source continuation forms
  a straight top-to-bottom main lane, with alternatives beside it. This is a
  layout heuristic, not proof of successful execution; equally long alternatives
  and loop edges remain available. Select an arrow for its complete conditions
  and supporting branch evidence. Contracts show source types, not runtime values.
  **Expand** opens nested operations inside their parent boundary. **Collapse**
  retains surrounding context; **Focus flow** opens a responsibility's own flow
  with a connected parent overview. Back restores selection, expansion, zoom and
  scroll. Full Go documentation and inline comments remain in selected evidence;
  node purposes retain the complete opening paragraph rather than an arbitrary
  character limit. Missing source facts remain explicit boundaries.
- Entry groups and flow nodes show comparable coverage, maximum cyclomatic
  complexity and highest supported architecture concern. Group scopes contain
  unique represented operation definitions, not every reachable function:
  repeated calls do not multiply their coverage or complexity. Statement blocks
  count once, coverage is weighted by statements, and unassessed members remain
  distinct. Select a flow node for exact membership and individual measurements.
  Hygiene is shown once in the snapshot summary, not as a local node measurement.
- **Explore**, **Assess**, and **Evidence** group the review destinations in
  persistent navigation. Structural views keep a compact explorer, a primary
  diagram, and independently scrolling details in separate columns. Selecting a
  package exposes its filterable functions; breadcrumbs return to its ancestors.
  **Review map** is always available under **Assess** for repository-wide
  comparison. Logical flow, data flow, and call relationships require a selected
  function; control flow additionally requires its source graph. The selected
  view is highlighted independently of whether other views are available.
  Large package maps open at a
  readable scale; **Fit all** explicitly fits the complete diagram.
- **Details** opens/closes the coordinated inspector; **Close details** or Escape
  dismisses it. Documentation and diagnostics use a wider reader without losing
  code context. Back restores navigation context and explorer position.
  **Repository & scope** reveals collection controls without leaving the
  diagram. **View options** contains display settings and explicit test-evidence
  collection/import actions. **Inspect operation map** and **Inspect function
  steps** remain explicit detail actions, not the default review experience.
  **Fit overview** considers both diagram width and height; scrolling and
  independent zoom remain available when a readable overview exceeds the viewport.
  Application flow diagrams use the same gestures as the other graphs: scroll
  to pan, Shift-scroll or pinch to zoom, drag the background to pan, and drag
  nodes to rearrange. Alt-arrow moves the focused node; Reset layout restores
  its source-derived arrangement.
- **Tutorial** provides a short, contextual walkthrough of the open view without
  changing the selection or executing analysis or tests. It starts on first use
  in this browser. Skip, Escape, or Finish dismisses it and remembers that choice;
  the toolbar button replays it anytime. Back/Next and keyboard focus stay within
  the walkthrough while it is open.
  Each view has its own lessons: what question it answers, how to read its
  cards/arrows or axes, which tools reveal more detail, how to inspect source,
  and which perspective to use next. Measurement lessons distinguish unknown
  evidence, coverage, complexity, supported-rule findings, and check outcomes.
- In the optional function walkthrough, expand a
  local call at its occurrence; its body leads back to that caller's normal
  continuation. Repeated calls remain distinct, and package boundaries do not
  reorder behavior. Nested argument calls precede the outer invocation when
  source evidence establishes their evaluation order.
- The overview opens the project's root README (or the shallowest collected
  document), with a selector for all collected Markdown. Endpoint, package,
  function, CFG and SSA details expose **Related documentation** with explicit
  association reasons: route, source/package path, qualified symbol, quoted
  package name or a colocated document. These are textual matches, not inferred
  semantic relevance or proof a document is current. Opening a document preserves
  code context; Back restores it. Markdown renders locally with sanitized HTML;
  document provenance remains visible.
- Endpoint details retain registration and call-site source, Go documentation,
  external/dynamic boundaries, metrics and existing CFG/SSA drill-ins. Back
  restores the walkthrough's expansion, selection and viewport; drill-ins
  include a return link to the originating endpoint. **Startup & wiring**
  provides source-linked `main` and `init` entries. Package and review maps remain
  available, but are no longer the required starting point.
- HTTP discovery currently supports type-verified `net/http.Handle`,
  `HandleFunc` and their `ServeMux` methods. Constant expressions are evaluated,
  local function/method/literal handlers and concrete `ServeHTTP` implementations
  are linked, and duplicate registration sites remain separate. Dynamic route
  expressions, function variables, interface targets and middleware results
  remain explicit unknowns. This is a registration inventory, not proof routes
  are mounted or reachable. Other routers, mounted prefixes and indirect
  registrations are not resolved. A module with no supported registrations
  shows that limitation and offers startup/package exploration rather than
  claiming it has no endpoints.
- Walkthrough arrows describe possible execution, not a recorded runtime path.
  Short-circuit expressions remain explicit ordering boundaries. Older
  snapshots without call-order or invocation evidence do not gain speculative
  sequencing. Deferred registration and goroutine launch stay in caller order;
  their bodies use dashed inspection links, not synchronous return arrows.
  Panics terminate the displayed path; deferred execution, recovery and goroutine
  scheduling are not simulated. Missing CFGs and expansion guards are explicit.
- **Logical flow** is the default when opening a function from a package or
  search result. Follow one implementation at a time instead of expanding a
  framework's entire call neighborhood. Receiver-aware method names distinguish
  implementations. Calls into other local packages appear first; same-package
  calls are explicitly collapsed when cross-package targets exist, and otherwise
  shown immediately. This is structural grouping, not a business/helper judgment.
  Expand supporting calls, inspect external/dynamic boundaries, and filter or
  page through every target. Search includes receiver types.
- Function values have separate dashed **reference** links, not invented call
  edges. These let reviewers follow registrations, callbacks or assignments into
  their local implementations without HTTP-specific logic. The originating
  source line is visible and searchable; the destination retains exact call or
  reference sites for inspection. The clickable navigation trail distinguishes
  calls from references, while Back restores the previous filter, expansion,
  page and viewport. CFG, SSA, source, documentation and selected-test evidence remain
  available. This view does not establish callback execution or execution order.
- **Review map** plots functions by raw cyclomatic complexity (right) and
  statement-weighted coverage (up). Unknown coverage has its own gray band,
  separate from measured zero. Numbered markers contain all functions with
  identical measurements; select one to choose a member. Filter by package or
  name, inspect the existing gauges, then enter control flow or SSA.
  Back restores filters, selection and viewport. Points stay at measured
  coordinates rather than being draggable; background pan, zoom, Fit and Reset
  remain available. Axis scales remain stable while filtering. Callable
  declarations without measured complexity are counted as omitted. This map
  is evidence exploration, not a quality score or assertion-quality assessment.
- Package dependency diagram: select a package to explore its functions.
- Function call diagram: select a function to explore its callers and callees.
- Function control-flow diagram: ordered steps, conditional successors, loop
  backedges and returns. Follow a branch to its next step and inspect the
  associated comments and source. The initial viewport keeps steps readable;
  Fit shows the overview. Unreachable blocks can be shown separately.
- Consecutive simple declarations and variable assignments appear as compact
  statement blocks by default. Select a block to expand it or inspect individual
  statements; collapse it again from any member step. **Group simple steps**
  switches between compact and full control flow. Calls, receives, decisions,
  loop/branch boundaries and returns remain separate. This does not merge SSA
  value dependencies or make a block atomic. Member source ranges, comments and
  coverage remain available; block gauges count matching profile statements once,
  and missing member evidence is identified in the detail panel.
- Click through call neighborhoods, including cross-package calls.
- Dashed edges are labeled by view: unresolved calls in call diagrams,
  function-value references in Logical flow, and inspection-only expansion in
  endpoint walkthroughs. Structural call/reference arrows are not execution order.
- Scroll to pan diagrams horizontally and vertically. Shift+scroll or trackpad
  pinch zooms; zoom buttons and background drag-to-pan remain available.
  Fit, back navigation and symbol/comment search retain the current context.
- Drag individual nodes to rearrange any diagram; connected edges reroute
  around node rectangles where space permits. Background dragging still pans.
  Positions are retained while navigating the current snapshot. **Reset layout**
  restores automatic positions; **Fit** includes routed edge detours.
  Focus a node and use Alt + arrow keys (Shift for larger steps) to move it
  without a mouse. Overlapping nodes can obstruct routes; these edges are
  explicitly dashed red until the nodes are moved apart.
- Go documentation alongside selected packages/functions.
- Inline comments in line-linked source drill-down and links into VS Code.
- Markdown documentation rendered, searchable and linked to code context.
- Evidence panel with collection commands, snapshot hashes, revision, warnings,
  and explicit analysis limitations.
- Local JSON snapshot API for supporting agent exploration.
- Import standard Go statement coverage and overlay matching blocks on control
  flow. Gray means missing evidence, not zero coverage. Mixed blocks are distinct.
- **Collect selected unit tests** runs exact names once with standard coverage
  and, optionally, a standard Go execution trace from that same run.
  No custom recorder, source rewriting, patched compiler or external collector
  is required.
- Syntax-based cyclomatic complexity beside each function flow.
- Compact JustGage semicircular widgets (32px wide in details, 25px on nodes),
  always ordered Coverage then Complexity. The entire face shows the current
  status color, with a white, dark-outlined needle and numeric labels.
  High complexity stays visibly red rather than shrinking to a tiny sector.
  Coverage is weighted
  by matching profile statement counts, not by the number of blocks; missing
  evidence is gray `?`. Complexity displays its actual count, not a trust score.
- On-demand **Value data flow**: select a function, then follow SSA value
  definitions and direct operand uses. Start with a small neighborhood and
  expand upstream, downstream, or to the whole function. Branch merges and
  loop-carried definitions remain explicit.
- Known local SSA calls show a drill-in marker: select the call to open the
  callee's control flow or value data flow. Back restores the caller's selected
  value and slice. Calls remain opaque data-flow boundaries; navigation does
  not infer argument-to-return propagation. Unknown dynamic and external
  targets have no speculative local links.

The interface is an application, not an offline report. Source inspection is
available when needed, but the main view is the navigable diagram.

### Gauge widget dependency

The MIT-licensed JustGage 2.0.1 browser distribution and license are vendored
under `internal/webui/vendor/` and embedded in the Go binary. There are no CDN
requests, runtime npm requirements, or transitive JavaScript dependencies.
After changing the pinned version in `package.json`, regenerate the vendored
files with `npm install --ignore-scripts` and `npm run vendor:gauges`.
For an unchanged lockfile use `npm ci --ignore-scripts`. Run frontend tests with
`npm test` (or `node --test internal/webui/*_test.js` without installing npm dependencies).

## Evidence boundaries

Static calls are resolved using Go syntax and type information, not AI.
Interfaces, function values, and reflection are not a complete whole-program
call graph. Unresolved and external call sites retain their locations and
resolution explanation. Ordinary analysis excludes test sources and inactive
build tags. Selected test sources retain provenance without becoming production
components. Type-check
errors remain visible.

Documentation is not certified as correct. Locally vendored Marked and DOMPurify
render sanitized Markdown; scripts, forms, embedded frames, unsafe links and
images are blocked. Local document links open collected documents without
fetching arbitrary files; external HTTP(S) links require a user click.
Source and local paths are returned to
the browser, so do not expose this service beyond loopback or share snapshots
without reviewing their contents.

### Control flow is not observed execution

The function-flow view uses `golang.org/x/tools/go/cfg` and source-linked Go
syntax. Its edges show
possible progression, not which branch a test took. Its source-linked call
buttons enter another function's flow; they do not prove a runtime target for
an indirect call. Graph-specific limitations are shown beside the function.

The architectural view uses hits in source regions unique to a relevant arm
to infer exercised alternatives. Ambiguous regions, empty arms and switch
fallthrough remain conservative. Covering both alternatives does not establish
one scenario, message lineage, or meaningful assertions.

Select **Collect selected unit tests**, choose a package and exact top-level
test names, and explicitly authorize execution. Only those unit selections run;
the action never starts the application, benchmarks, integration/smoke suites
or repository hooks. Tests remain trusted executable code: their helpers,
initializers and TestMain can have side effects.
Collection defaults to the selected component's package. On multi-module
reviews, refreshed evidence stays attached to that module; other modules and
their review failures are preserved.

Optional standard runtime evidence uses `go test -trace` and `go tool trace`.
Scheduler launch/block/wakeup events retain goroutine relationships and
event-associated production stacks. Source-line candidates link asynchronous
boundaries without inferring every normal call or message. This can distinguish
competing launchers and clarify concurrency, not reconstruct exhaustive call
ordering. The adapter currently supports inspected GoID-form
`go tool trace -d=parsed` output (verified with Go 1.27). Unsupported tool/format,
missing or truncated traces and mismatched source identities are explicit;
raw artifacts survive and architecture/coverage remain usable.

Run focused fixtures with
`sh scripts/test-functional-walkthrough.sh ui`, `system`, `branches`,
`execution`, `runtime` or `api`. The `all` selector runs frontend tests,
Go Review's race suite, vet and build. Opt-in `builder MODULE_DIR EVIDENCE_DIR`
uses the inspected exact acceptance selections and retained UI; `builder-ui
EVIDENCE_DIR` reopens them without target execution. `wasm MODULE_DIR
EVIDENCE_DIR` evaluates the secondary source case and reports its component
unit-harness limitation without native execution or deployment.

### Coverage interpretation

Import a profile produced using the standard Go toolchain:

```sh
go test -run='^(TestCreate|TestReject)$' -coverprofile=/absolute/path/coverage.out ./selected/package
```

Then use **Import coverage**. Imported source freshness is unverified.
Colors describe overlapping statement blocks, not branch/path coverage.
Passing tests and executed statements do not prove useful assertions.

Gauge colors use a deeper kelly-green end and smoothly blended display conventions, not correctness
thresholds: coverage moves from red at 0% through orange at 40% and yellow at 60%
to green at 80–100%; complexity moves from green at 1–5 through yellow at 10 and
orange at 20 to red at 25+. Complexity's needle saturates at 25 while the number
remains actual. Hover or focus gauges for scope and interpretation. Package
coverage includes only matching profile statements, not assumed zeroes for
files absent from the profile. No aggregate quality score is computed.

### Value data flow

The value view integrates `golang.org/x/tools/go/ssa`. Arrows connect a value
definition to an instruction that directly uses it; they do not represent
execution order. A phi node merges possible incoming definitions, not an
observed value or proof that a path is executable.

Calls are opaque boundaries: arguments feed the call instruction, but the
diagram does not infer a relationship between a particular argument and a
returned value. Loads, stores, maps, channels and other memory operations are
marked boundaries. No alias analysis or store-to-load lineage is claimed.
Closures expose captured inputs without inventing their origins in another
function. Runtime values are not captured.

Collection is local and on demand, requires matching snapshot inputs, and
never executes target tests. Unsupported functions, type errors, and changed
source produce visible errors rather than an invented graph. Some compiler
instructions have no exact source position; the UI identifies this instead of
assigning a misleading location. Nearby comments are attributed as nearby
source text, not certified explanations of SSA operations.

Supporting clients can read `/api/snapshot`, then POST
`{"symbolID":"…","sourceSHA256":"…"}` as `application/json` to `/api/dataflow`.
The response contains the selected graph and its collector/command provenance;
it does not mutate the snapshot or start tests. Requests for an older snapshot
are rejected. Graphs are held only in the browser's current snapshot session.

Interprocedural data flow, alias analysis, mutation/test-strength measures,
use-case mapping, semantic indexing, and revision/PR review remain in the full
product scope. None are silently replaced by an AI assessment.

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/go-review
node --test internal/webui/*_test.js
```

Transient test modules use `t.TempDir()` outside the checkout so editor test
discovery does not race their cleanup. Keep `TMPDIR` outside the repository.

Requirements and rationale: [intent](docs/project-intent.md),
[constraints](docs/project-constraints.md), [design](docs/design.md).
