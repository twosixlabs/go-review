# Go Review: evidence-first workbench

Status: implementation design derived from the agreed requirements. This is
not a claim that every roadmap capability has shipped. See the README for
available commands and current limitations.

## Optional chat

The optional chat service uses an explicitly configured OpenAI-compatible provider. Provider
configuration lives in the OS user configuration directory, outside reviewed
repositories. HTTP is allowed for loopback; non-local endpoints require HTTPS.
Optional `apiKey` or `apiKeyEnv` supplies bearer authentication. Keys never enter
browser status/history. Redirects and automatic provider fallback are disabled.
The UI identifies external providers and discloses source transmission.

Each chat turn owns an in-memory SDK client connection to the same MCP server
registration used by the public endpoint. Tool schemas are discovered, not
reimplemented. Snapshot references are validated against retained reports; the
agent can read across retained modules without changing UI selection or
executing target code. Structured tool results avoid
duplicating the SDK text fallback. Configurable active-context and step budgets,
bounded responses, and timeouts control resource use without a transcript
message-count cutoff. The default active context budget is 256 KiB including
tool schemas; browser-to-server requests are bounded to 4 MiB. Turn-owned exact-evidence
archives compact old tool results between rounds; a bounded recall tool restores
paged text. When the active budget requires displacing old conversation,
its exact text is also archived and retrievable, never promoted to trusted
source evidence. The browser retains the full transcript independently of the
active model context. Archives last for the current turn; later turns requery
source evidence. Browser Stop propagates
cancellation through model and MCP requests.

The browser sends current navigation context and receives newline-delimited
stream events for text, tool activity, validated navigation, errors and completion.
History belongs to the browser session and snapshot, not global server state.
Model assessments remain chat text, never measured findings or gauge inputs.
Comments, documents and tool output are evidence, not privileged instructions.

Navigation uses a separate allowlisted `navigate_ui` function, not a mutable
public review MCP tool. Server validation resolves retained IDs through the
shared query engine; browser validation repeats against current evidence.
An explicit movement request supplies per-turn navigation permission through
the composer, without a checkbox. Unrecognized or unsolicited destinations
require a Show in diagram click. Late automatic actions are rejected after a
manual context change. Existing entry/focus/expansion/document/navigation
handlers preserve evidence semantics and Back history. The next request includes
the last confirmed browser navigation; dispatch alone is not proof of movement.
Each turn refreshes the current evidence through MCP rather than treating prior
model prose as facts. Ordinary responses require no navigation decision or
no-op call. Expansion
and focus remain separate operations. Retrieved document, symbol and entry tool
activity provides evidence links independently of model citation formatting.

Each response has one expandable activity section for tool operations and public
progress summaries, distinct from its final answer. Working state spans model
waits, tool calls, and response generation. Provider reasoning channels are not
rendered. Enter sends except during IME composition; Shift+Enter inserts a line.
No MSS/Starlark mediation or generated-program execution is introduced.

Tool-shaped prose is never parsed or executed. If a local template prints
unparsed tool markup, a visible protocol warning requests a native-tool
correction, at most twice; repeated failure terminates the turn explicitly.

## Relationships

```text
Target Go module and documentation
    -> standard Go package discovery
    -> source/symbol/comment and static-call analysis
    -> architectural components, information contracts and alternatives
    -> imported or explicitly selected-unit statement coverage
    -> optional standard Go runtime trace from the same selected-unit run
        -> evidence snapshot
            -> local interactive workbench for human review
            -> structured export for supporting agent queries
```

The target repository is an input. Generated reports and execution-evidence
artifacts live outside it. There is no dependency from Builder on Go Review.

## User experience

Application starts with project-specific entry points and separate initialization. Explicit navigation
opens architectural system context, component neighbourhoods and
relevant operation alternatives. Collapse supporting implementation and retain
source-linked broad information contracts behind concise arrows. Arrows describe
information exchange, not temporal ordering or proven object lineage. Package
groups provide structural navigation without inventing business roles.

The retained inventory uses WIT/component configuration for Wasm, main for CLI,
public callable declarations for standalone libraries, and supported HTTP registrations
for services. Init wiring is supporting initialization, not an external entry.
Selecting an entry opens an entry-scoped overall control/data projection:
transport processing, component operations, downstream contracts, response
preparation and scheduled background work. Source CFG paths connect stages;
hidden helper regions retain their branch alternatives without becoming
statement cards. Repeated call occurrences remain distinct. The first component
level supplies evidence for compound responsibilities. The default is a connected
compound diagram. Source ownership groups nested operations under their caller;
expanding a group reveals children within its boundary rather than replacing the
overview. Asynchronous bodies remain independent roots in a separate lane, without
an inferred join. The longest forward source continuation forms a vertical spine;
early exits and other alternatives occupy a side lane. Expansion applies the
same layout inside each source boundary, keeping parent faces aligned even
when a boundary widens. DFS backedges do not contribute to spine length, but all
edges remain inspectable. Ties follow stable source order and are disclosed in
the layout tooltip. This structural heuristic does not establish a successful
execution or rank a branch's likelihood: arrows
establish possible continuations. Selecting an arrow exposes complete source
alternatives and contracts without presenting their union as a conjunction.
Selecting a node opens full documentation, inline commentary, boundaries, source
and test evidence. Node purposes preserve their entire opening paragraph.
Focus opens a deeper source projection while retaining a connected parent map;
Back restores the previous expansion, selection, zoom and scroll. The operation
map and function-level steps are explicit secondary actions.

The immutable raw scene also supplies hierarchy membership and gauge scope.
Topology preserves contextual call occurrences, while measurement uses unique
operation definitions represented by a group and its descendants. Coverage counts
each overlapping statement block once and weights by its statement count.
Complexity is the maximum measured member, not an average or sum. Architecture
uses the highest assessed supported-rule concern, with unassessed members counted
separately. Missing source boundaries remain visible even without a resolved
symbol. Exact membership and member evidence are inspectable. These are source
ownership scopes, not claims to have discovered domain responsibilities or every
reachable helper. Snapshot hygiene remains in the persistent summary rather than
being copied onto each node.

The entry inventory groups registrations spatially, with handler-scoped and group
gauges; startup-only modules retain main/init entry discovery. Project
documentation remains coordinated with the overview. A compact persistent bar
provides module selection, Application/Back, search, inspector visibility,
repository controls and Tools. Tools discloses diagnostics and alternative
structural/implementation views; collection progress and errors remain visible.
Local expansion changes the size of its parent lane, not unrelated columns.
Large expansions remain scrollable and can be focused; automatic initial zoom is
bounded to preserve readability, while explicit Fit considers both dimensions.
Inferred dispatch, unknown expression order, recursion, missing control flow and
projection limits remain visible. Background work never acquires an inferred
synchronous return edge. Stage contracts describe source types and names, not
runtime object identity. Both overview navigation and code drill-down retain
entry context and Back history without executing tests.

The optional function walkthrough shows registration, handler and possible paths. Package
dependencies, function relationships, CFG/SSA and metrics remain secondary
exploration tools. Exact source, call sites and Markdown documentation are
available as drill-down. Historical snapshots without architectural facts retain
their entry-discovery view and never trigger execution.

The overview evidence panel shows the repository README and a document selector,
not tutorial copy. Documentation discovery defaults to the nearest enclosing
Git repository (including worktree markers), otherwise the Go module; an explicit
documentation directory overrides it. Hidden directories, symlinks, dependencies
and generated artifact directories remain excluded. Documents retain their root,
relative path and content hash.

Code details surface documents associated through exact route mentions,
source/package paths, local source links, receiver-qualified symbols, quoted
package names or package colocation. Every match shows its reason; titles alone
do not establish relevance. All collected documents remain browsable when no
explicit match exists. Document selection survives overview filtering and Back;
the reader leaves graph context intact and dismisses when a new code location is
selected. Locally vendored Marked and DOMPurify render document structure with
an allowlist of passive HTML tags and attributes. Scripts, styles, forms, frames,
images and unsafe link protocols are excluded. Relative document links resolve
only against collected documents; missing targets are reported. External links
open only on user action. Content is never an executable instruction or verified
behavioral claim.

HTTP evidence is collected from type-verified net/http Handle/HandleFunc calls,
including ServeMux methods and method expressions. Compile-time constant
patterns retain their evaluated value and original expression. Local functions,
method values, function literals, HandlerFunc conversions and concrete
ServeHTTP methods can link to snapshot declarations. Function variables,
middleware return values, interface dispatch and external implementations do
not acquire speculative local targets. Each registration retains a stable
source identity, owning declaration, API/receiver expression and source range;
duplicate patterns are not merged. The inventory establishes source
registrations, not server startup reachability, middleware effects or mounted
paths. Other routing APIs and indirect registration calls are explicitly outside
the current collector's scope.

The endpoint walkthrough composes source CFGs, keeping the registration root.
Every call occurrence has its own identity, including repeated calls to the same
function. Expanding one synchronous occurrence inserts its local CFG between the
call site and the caller's normal continuation. Branch alternatives, early
returns and loop backedges retain their original evidence; longest-path layout
places joins after their alternatives. Package grouping never replaces control
flow in this view.

The collector records call evaluation order after visiting argument expressions,
and separately classifies synchronous invocation, defer registration and
goroutine launch. Multiple calls within a source step become an ordered sequence
only when CFG metadata excludes short-circuit evaluation. Short-circuit
expressions and older snapshots without sufficient metadata remain inspection
boundaries. Argument calls in go/defer statements remain synchronous; the
scheduled outer call does not acquire a synchronous return edge. Inspection-only
bodies are labeled explicitly. Panics end a path rather than returning through
the shared CFG exit; recovery, actual deferred execution and goroutine scheduling
are not simulated. Expansion requires an explicit occurrence selection and is
guarded by depth and scene size; unavailable flow stays an explicit boundary.

Back retains expansion, selection and viewport, while CFG/SSA and relationship
drill-ins carry a return link to the walkthrough. Call-site source, Go docs,
comments, metrics and external/dynamic reasons remain inspectable. The detailed analysis toolbar is available through Tools, rather than competing
with primary review navigation.

Function selection starts with a focused logical-navigation view, not an
expanded compiler or framework neighborhood. Each card opens a verified local
implementation; receiver-aware titles make methods recognizable. Cross-package
targets appear first. Same-package calls are explicitly expandable when other
packages are called, or immediately visible for same-package-only functions.
No package is automatically classified as business logic or infrastructure.
Repeated targets share a card but retain every source site. Target filtering
includes original call/reference source lines, with explicit pagination.
External and dynamic calls remain inspectable boundaries rather than being
silently discarded.

The collector classifies callable references by syntactic position: invocation
target versus function value. This distinguishes both occurrences in
`use(use)` and handles immediately invoked versus passed function literals.
Function-value references provide dashed navigation into callback, assignment
and registration implementations without inferring invocation. Older snapshots
with missing classification retain explicit unknowns. A trail distinguishes
reference hops from static calls, and Back restores the previous view state.
Exact source, documentation, metrics, CFG, SSA and selected-unit evidence remain
available. Neither card placement nor the trail establishes execution order.

The first delivery is a local interactive web workbench, not an offline
report. A Go backend analyzes selected modules and serves structured evidence.
An embedded browser interface renders navigable package and function diagrams,
with pan/zoom, search, call neighborhoods, documentation and source drill-down.
No remote assets or model are required. The scope of the first slice is limited
by the number of integrated evidence collectors, not by replacing interactive
exploration with text reports.

All diagrams share obstacle-aware edge routing and node manipulation. Dragging
a node updates connected routes without activating navigation; background
dragging pans. Retained positions belong to the current source snapshot and
diagram identity, not to source files. Reset discards only that diagram's manual
positions; Fit includes nodes and edge detours. Node keyboard movement provides
an alternative to dragging. Where overlapping rectangles prevent routing, the
obstructed edge is explicitly styled rather than claimed to be unobstructed.

Visual metrics preserve evidence units: statement-weighted coverage percentage
and raw cyclomatic count. Locally bundled JustGage semicircular widgets use a
fully status-colored face and a white, dark-outlined needle so high complexity
remains conspicuous even at the end of the scale. Coverage always precedes
complexity in both graph and panel. Colors blend between explicit palette anchors. Unknown
measurements stay neutral; color anchors are disclosed presentation conventions
rather than readiness judgments.
Analysis explanations are progressively disclosed; errors remain prominent.

The review map reuses the measured function complexity and coverage helpers
without another collector. Raw complexity is linear and uncapped on the
horizontal axis; matching statement-weighted coverage is on the vertical axis.
Unknown coverage occupies a separate gray band, never the zero-percent row.
Functions with exactly coincident metrics share a numbered marker and remain
individually selectable in a paged list; coordinates are not jittered. Larger
markers indicate multiplicity, not risk. Package/name filters retain the
snapshot-wide axis scale. Declarations without complexity measurements are
explicitly counted as omitted, and shared source ranges do not imply independent
coverage. Function drill-in and source navigation retain the map's
filters, selection and pan/zoom in Back history. Plot points cannot be dragged
away from measured coordinates. No composite score or automatic readiness
judgment is introduced.

Function exploration now separates static statement control flow from call
relationships. Static flow presents conditional successors, loop backedges,
returns, and source-linked comments using the standard Go analysis ecosystem.
Users can follow a successor or enter a called function without reconstructing
the flow from entire source files.

Control-flow grouping is a reversible display transformation, not a new
analysis. The collector marks simple declaration/identifier-assignment steps
with their original CFG block identity. Calls (including conversions), receives,
closures and short-circuit expressions are excluded. The browser contracts only
unbranched, unlabeled sequence edges between source-ordered marked steps in
the same block and with the same reachability. Decisions, joins and loop
boundaries cannot be crossed. Blocks show a preview and count; expanding restores
the original steps and edges. Source/comments remain attributed to individual
members; coverage uses their range union, not the enclosing span or just the
first member. Missing member evidence stays explicit. Expansion and selection
survive Back navigation; each compact/expanded diagram has its own retained
layout. These blocks are neither atomic execution units nor SSA value groups.

Value exploration is a separate, on-demand SSA view built with
`golang.org/x/tools/go/ssa`. Direct definition/use edges connect parameters,
constants, operations, branch merges, calls and return instructions within one
function. Initial exploration shows only the selected value's neighborhood;
engineers can expand upstream/downstream without starting with an entire
module's value graph. Source, nearby comments and exact collection provenance
remain accessible. Collection errors and snapshot changes are visible.

SSA operands do not establish memory lineage or executable paths. Calls,
loads/stores and other memory operations are marked opaque boundaries; this
increment does not propagate data across callees, solve aliases, or infer runtime
values. Statically known call targets link to matching snapshot declarations,
including local-package imports, methods and closures where SSA identifies the
declaration. Selecting a call offers separate control-flow and value-flow
drill-ins; Back restores the caller's selected value and neighborhood. These
links are navigation, not cross-function data edges. Dynamic and external
targets remain explicitly opaque.

Imported statement-coverage blocks overlay matching flow-step source ranges,
with unknown, uncovered, covered and mixed states distinguished. A covered
condition does not establish that both outgoing edges were exercised.

Coverage hits unique to a conditional arm support inference that it executed.
Shared successors are not arm witnesses; empty arms and fallthrough retain
conservative reasons when evidence is ambiguous. Relevant alternatives show
exercised, not-observed or unknown. Both arms covered does not establish one
chronological path. Types, names and attributed documentation describe broad
information without recording every variable.

Review runs all in-scope Go tests when no unit selections are saved; exact saved
selections restrict execution. Both modes collect standard cross-package coverage,
once per package or explicit selection. Actual matched tests, outcomes, command
diagnostics and source/scope identity are retained. Coverage from several tests
is selection-level evidence, not attribution to each passing test. Failed runs
retain partial evidence and errors; skipped or unmatched tests are not passes.
Initialization, TestMain and dependencies can execute: this is trusted-code
execution, not a sandbox. Normal Go test execution includes examples and fuzz
seeds, not benchmarks or fuzz campaigns. Integration and smoke tests must be
excluded explicitly if undesired; the application is not launched separately.
Repository verification requires separate
authorization. Browsing and reopening retained results never run collection.

Evidence storage is latest-only per canonical repository root. Publishing a new
review, selected-test refresh, or static analysis removes superseded run
directories. A selected-test refresh first copies only artifacts referenced by
the other current module snapshots into the replacement, so deleting the old run
does not break current coverage or runtime evidence. Cleanup is restricted to
owned, direct, non-symlink directories with matching repository manifests; errors
are surfaced rather than claiming successful cleanup.
Checks not run are disclosed separately from failed checks and do not produce an
attention status. The UI shows actionable outcomes, not internal cache paths.

Documentation is displayed verbatim and attributed. Automated documentation
presence or reference checks do not prove semantic agreement with the code.
Do not fabricate semantic links from document titles or use-case names.

## Evidence collection

### Review workspace interaction

Application review remains the primary entry experience. Other review views use
explicit explorer / canvas / inspector grid areas, never implicit grid placement.
Explorer and inspector scroll independently; the canvas receives the remaining
width. Narrow screens use a dismissible inspector overlay. Large package maps
start at readable node scale, with a separate Fit all action for the overview.

Persistent Explore, Assess and Evidence groups expose destinations; function
views operate on the selected callable, with unavailable actions disabled and
explained. Display preferences and code-execution controls remain secondary.
Clickable ancestors and Back preserve review context. Documents and diagnostics
use a wider reader, while close controls and Escape return space to the diagram.
No navigation gesture implicitly executes target-project tests.

This follows [NN/g progressive disclosure](https://www.nngroup.com/articles/progressive-disclosure/)
without hiding primary navigation, [NN/g breadcrumb guidance](https://www.nngroup.com/articles/breadcrumbs/)
for wayfinding, [Carbon empty-state guidance](https://carbondesignsystem.com/patterns/empty-states-pattern/)
for explicit missing/unavailable states, and [WCAG target-size guidance](https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html)
for operable controls. Browser validation covers actual pane bounds, readable
node size, source drill-down and return paths, not only DOM presence.

Use the Go toolchain for module/package selection and standard coverage
profiles. Use Go syntax and type information for symbol identity, direct calls,
locations, and documentation. Compute clearly named complexity measures from
syntax, with a documented definition.

Static call analysis must state its resolution limits: interfaces, function
values, reflection, generics, and dynamic loading cannot be silently represented
as fully resolved behavior. Go calls and deferred calls must not imply a
globally sequential order.

Coverage imports associate blocks with matching source. Missing coverage is
different from zero coverage. Coverage collected without snapshot provenance
must be identified as externally supplied evidence, not assumed current.

## Optional standard runtime evidence

The selected-unit action can also request `go test -trace`. Standard
`go tool trace` output supplies event-associated production stacks and
goroutine launch, blocking and wakeup relationships, not every ordinary function
call or every message's causal path. Source-line matches can disambiguate static
asynchronous candidates without fabricating a global sequence.

Retain the raw trace, actual tool version, parsed output and diagnostics. The
adapter accepts only inspected formats; unsupported, incomplete or stale traces
remain explicit rather than becoming empty successful observations. Usable
coverage and architecture survive a runtime-decoding failure. Source mismatch
withdraws execution annotations.

There is no custom AST-overlay recorder, argument snapshotting or instrumented
baseline rerun. Scope-exclusion overlays remain for enforcing the selected
review policy without changing target source. Old custom trace fields are
historical snapshot compatibility only, not a live API or interactive view.

## Provenance and reliability

Evidence includes source locations, stable symbol identifiers, source hashes,
toolchain identity, collection commands/configuration, and timestamps where
applicable. Normalize presentation order, not observed runtime ordering.

Do not swallow tool failures or display empty output as a successful clean
assessment. Inaccessible source, unresolved calls, mismatched coverage, failed
tests, and truncated runtime traces require visible diagnostics.

Generated HTML treats source and documentation as untrusted display text.
Do not interpret embedded scripts or fetch remote document assets.

## Read-only agent exploration

The workbench's `/mcp` endpoint uses the official Go MCP SDK's stateless
Streamable HTTP transport behind the existing loopback Host/origin protection.
It exposes only retained reports, not the target filesystem or execution APIs.
Each response identifies the full-report digest, source hash, module and
collection time. Requests pin that digest; changing the UI's selected module
does not redirect a pinned MCP exploration.

The UI and MCP share entry inventory, source flow contraction, source-owned
hierarchy, expansion, metric scope and architectural projection functions.
New reports retain classified entry evidence rather than having the browser
guess project type from HTTP registrations alone. WIT exports and available
component/wasmCloud configuration establish Wasm entries, verified binding
evidence links them to Go implementations, and unresolved exports remain
explicit. CLI main functions and standalone libraries' exported callable API
use the same entry representation. Application helper packages are not
reclassified as libraries. Go initialization is a separate collection.
WIT/configuration text is retained and included in snapshot source identity;
explicit evidence sections and the document reader disclose it progressively.
Old reports retain their original identity and show a reanalysis notice instead
of reading live project files while browsing.
Go runs these embedded JavaScript modules using an isolated goja runtime with
no Go, filesystem or network bindings. Published reports are immutable; each
runtime owns a JSON copy. Runtime access is serialized and request cancellation
interrupts projection without leaking interruption into the next request.
Caches retain only reports currently available in the workbench.

Contextual occurrence IDs are meaningful only with their entry, focus path and
expansion context. Focus retains the connected parent map. Inspection pages
operations and alternatives without dropping membership. Discovery returns
counts, bounded owner previews and gauges rather than hidden member IDs,
full CFGs, logs or architectural catalogs. Explicit `section` queries page
collections; `nodePage` / `edgePage` indicate incomplete topology, not a smaller
projection. The overview exposes module-wide maximum owners directly, avoiding
package scans to explain a module gauge.

List pages default to 10 items and additionally enforce a 6 KiB serialized item
budget. Cursors advance by actual returned items. Large text is explicitly
paged by Unicode character offset under a serialized byte budget, including
JSON escaping; comments expose an index for full text retrieval. Source
excerpts retain original ranges and hashes. The shared query and MCP envelope
each enforce a 32 KiB ceiling and return explicit narrowing errors for oversized
results, never silently drop evidence. The snapshot catalog is also paged.
Chat checks its initial evidence refresh and every subsequent tool result
against 32 KiB, and rejects message context above 64 KiB before the provider call.
Static alternatives, coverage witnesses and standard runtime observations retain
their distinct semantics.

An MCP-only review tests whether this information is sufficient for meaningful
navigation and defensible findings. It does not prove a visual layout is usable,
nor establish that passing checks make the reviewed system well built.

## Validation

- Fixture modules establish symbol/source identity, docs, static calls, and
  unresolved indirect-call behavior.
- Complexity and coverage tests check exact expected values and block ranges.
- Exact selected-unit tests check scope, outcomes, source identity and
  coverage-inferred alternatives without rewriting the target checkout.
- Standard runtime tests check actual launch/block/wakeup evidence, format
  diagnostics and ambiguity without claiming complete call ordering.
- Browser/API tests check source escaping, navigation, interactive branches,
  statement-coverage distinctions, architectural contracts and alternatives.
- Build and vet the tool and analyze the actual Builder server.
- Verify the actual interactive experience, not only individual collectors.

## Subsequent capabilities

The shared evidence foundation should support revision comparison, PR/MR
review, requirements/use-case-to-test mappings, mutation-testing integration,
interprocedural/alias-aware data-flow backends, and optional semantic discovery.

Use-case completeness requires an engineer-reviewed inventory. Mutation
survivors require review, including equivalent mutations. Neither becomes an
automatic correctness score.

Future AI assistance must cite the same artifacts available to humans and label
all semantic suggestions separately from measured evidence. The non-AI
experience remains complete without it.
