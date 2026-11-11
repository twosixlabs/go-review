# Change-focused Application review

Status: all five design sections approved in conversation; consolidated
specification awaiting final written review. Design only, not authorization
to implement. Proposed interfaces remain hypotheses to validate through
functional implementation.

## Approved relationship map

- Complete revision-to-revision change inventory: the authority for accounting
  for changes; feeds all review perspectives and progress.
- Change Review: an additional Explore destination, independent of Application;
  presents affected flows, shared implementation changes, and other changes
  from that inventory.
- Affected flows: reuse Application's source-backed entry and hierarchy model;
  connect changed implementation to application context.
- Shared implementation review: one change identity with links to multiple
  affected flows; implementation review does not certify every flow consequence.
- Other changes: preserve tests, documentation, dependencies, configuration,
  excluded code, and unsupported mappings as visible review work.
- Focused before/after evidence: supports the selected change without making
  two entire application graphs or a merged graph the default workspace.
- Change workspace: consumes inventory, flow associations, and explicit
  revision-side evidence; integrates existing readers and function views.
  Complete change lists remain an alternative to graph navigation.
- Navigation context: retains comparison, revision side, module, flow,
  responsibility, selected change, expansion, and viewport across drill-downs.
  Existing Application selection remains independent.
- Revision collection: resolves immutable Git inputs and independently collects
  Before/After evidence under a common policy; supplies the complete inventory.
- Correspondence and association evidence: consumes both sides and inventory;
  supports matched declarations/occurrences and witnessed flow associations,
  with explicit ambiguity and analysis gaps.
- Comparison artifact: owns both sides, inventory, correspondences, provenance,
  and limitations; published atomically and retained separately from ordinary
  Application evidence.
- Shared comparison projections: consume canonical comparison evidence and
  existing side projections; feed UI and bounded read-only queries equally.
- Validated actions: consume explicit comparison/navigation identity and
  host-granted permissions; let UI and chat invoke the same navigation,
  collection, cancellation, and review-state workflows.
- Local review state: depends on comparison evidence identity; records unique
  item review and separate flow-context consideration, with atomic persistence
  and conflict detection. Feeds both UI and agent remaining-work queries.
- Collection job: owns work and cancellation until publication; transfers
  retained revision inputs to the comparison artifact and releases only
  tool-owned temporary resources.

## Purpose and boundaries

Make large proposed changes manageable by explaining them through the
application, rather than requiring reviewers to reconstruct behavior from
many file diffs. Change Review is an additional capability; existing
Application and detailed review tools remain available and unchanged.

PR/MR is an input source for revisions, not a second review-hosting interface.
Full Change Review capability is available through chat for navigation,
explanation, evidence retrieval, and explicitly authorized actions.

Initial scope includes committed local refs, PR/MR revision resolution,
complete change accounting, source-backed application associations, focused
comparisons, existing drill-down integration, local review progress, and
UI/agent parity.

Non-goals are provider comments/approvals, automatic test execution, opaque
risk scores, semantic-equivalence proofs, working-tree comparison, and a
historical analysis archive.

## 1. Organizing model

Application answers "How does this application work?" Change Review answers
"What changed, where does it matter, and what have I not examined?"
Neither replaces the other. PR/MR integration supplies revision inputs, not
comments, approvals, or another hosting-provider interface.

The initial view is a scannable list of affected flows and change descriptions,
not a repository-wide graph, metrics dashboard, or file-diff queue.

One inventory supports three navigable perspectives:

1. Affected application flows: recognized HTTP, Wasm, CLI, library, and
   initialization entries. Inclusion reasons distinguish direct implementation
   changes, entry/contract changes, and potentially affected flows with a
   source-supported relationship to changed code.
2. Shared implementation changes: review a changed operation once and follow
   contextual links from each supported affected flow. Inspecting the shared
   implementation does not automatically review its consequences in every flow.
3. Other changes: tests, documents, dependencies, configuration, excluded code,
   and changes without supported application mappings. Unmapped is not
   unimportant.

These are contextual views of unique changed items, not duplicate change
collections. Counts and progress distinguish unique changes from flow
occurrences.

Reviewable records aggregate related source spans under a declaration,
entry contract, or supporting artifact rather than making every Git hunk a
primary navigation row. Their links back to the complete inventory remain
inspectable. A record's review action covers an explicit set of inventory
items; aggregation cannot lose a changed span or duplicate its progress.

The primary loop is:

Choose an affected flow -> see changes in Application context -> inspect a
changed responsibility -> compare relevant before/after evidence -> return to
the same context -> continue with remaining work.

Application intentionally hides supporting helpers. Hidden changes must remain
visible through explicit changes-within indicators, even when displayed
topology is unchanged. The graph explains changes but never establishes
whether all changed material is accounted for.

### Alternatives

- Permanent side-by-side graphs halve reading space and require cross-panel
  searching. Use paired displays for selected comparisons instead.
- A complete before/after graph union creates clutter and risks implying paths
  that exist in neither revision. Do not make it the default.
- Use one change-focused Application view with focused comparison on demand.

## 2. Workspace and investigation loop

### Entry and orientation

Change Review is a persistent Explore destination beside Application. With no
comparison, it offers Compare revisions and Use PR/MR and explains what will
be compared. Existing Application evidence remains available.

A compact persistent header identifies Before -> After, scope, and evidence
limitations. Full revision hashes and provenance are available on inspection.
Provider metadata is an optional source label.

The overview starts repository-wide, including added and removed modules.
A searchable list uses the three approved perspectives. Rows show entry or
operation name, concise factual description, inclusion reason, and review state.
Unique totals and filtered-out work remain visible. Do not use opaque risk
scores, oversized summary cards, or invented business narratives.

If the Git inventory is empty, say No changes between these revisions and
identify both commits. If changes exist but no flows can be associated, open
the supporting change inventory and explain the semantic-analysis limitation;
never say there are no changes. Repositories with no analyzable Go modules
still have a usable complete inventory.

### Flow workspace

Reuse the review-list / Application-canvas / details structure:

- The list switches review areas.
- The canvas preserves grouping, Expand versus Focus, parent context,
  wheel/zoom/drag behavior, and keyboard navigation.
- Details explain what changed, supporting evidence, and other associated flows.
- Selecting inspects; Expand reveals children; Focus changes scope.
- Breadcrumbs identify comparison, module, flow, and focused responsibility.
- Back restores selection, expansion, viewport, and scroll.

### Comparison display

Default to After, annotated with Added, Changed, and Changes within labels.
Show enough quieter unchanged structure to explain changed paths. Different
arguments, conditions, contracts, or hidden helper implementations remain
discoverable even if displayed node labels and topology are unchanged.

Before / After switches preserve the selected matched responsibility where
possible. Missing counterparts are explicit and offer owning context rather
than silently selecting something else.

Removed stages appear in a separate Removed from this responsibility list at
the matched owner, not as invented After edges. Selection opens Before context.
Entirely removed flows open on Before and say Removed flow.
If there is no supported matching owner, expose the removal at flow level
rather than inventing an association.

Compare selected change opens a wide reader with relevant aligned Before/After
stages, conditions, contracts, source, or documents. It does not duplicate entire
application graphs. Coordinate anchors and scale where valid; expose unmatched
or reordered regions rather than forcing alignment. Targeted source diffs are
available here.

Changes in this flow lists direct, hidden, and shared changes independently of
diagram geometry. Associations must have source evidence; uncertain mapping
has a visible reason. Show unchanged context reveals more surrounding structure.
Collapsing context cannot hide the existence or count of changes. Unrelated
paths are collapsed, not marked removed.

### Existing tools and responsive behavior

Open Logical Flow, Control Flow, Data Flow, Call Relationships, source, and
documentation on an explicit revision side. A deleted symbol remains available
on Before. Preserve existing callable/evidence eligibility and explain
unavailability. Return restores comparison context; ordinary Application
retains its independent evidence and selection.

On desktop, the canvas owns flexible space; ancillary panes are collapsible.
Comparison readers use the wide-reader pattern. At narrow widths, navigate
between list, flow, and details instead of squeezing three panes together.
Opening chat must not leave an unusable graph.

Use text and symbols in addition to color. The change list provides keyboard
inspection independent of the graph. The contextual tutorial teaches revision,
change, context, and comparison semantics.

## 3. Revision and evidence semantics

The invariant is that every repository change is accounted for, without
manufacturing unsupported behavioral conclusions.

### Input resolution

Local Before/After refs mean exact selected commits. PR/MR inputs resolve
target tip and source head, with merge-base(target, source) -> source head as
the default comparison. Show target tip separately and label the actual
comparison before collection. Do not substitute a provider's synthetic merge
result. Provider adapters obtain inputs, not review judgments or progress.

Resolve refs once to immutable commits. Record repository identity, actual
commits, original labels, comparison policy, analysis tool version, environment,
and scope. Refresh is explicit. Moving branches do not mutate an open review.
Missing objects, unrelated histories, and ambiguous merge bases require
actionable errors rather than an arbitrary selection.

Initial scope is committed Git revisions. Working-tree changes are not silently
included; that would be a separate future input mode.
Provider-specific resolution remains a small adapter around the same immutable
revision request, not a separate review pipeline. Keep credentials in existing
machine-local authentication facilities, never in the comparison artifact.

### Complete inventory

Build inventory from Git trees/blobs before semantic grouping. Account for
text ranges, additions, deletions, renames, file modes, binaries, submodule
references, and limitations. Changes without textual hunks still have records.

Each item has Before/After locations or object identities, analysis eligibility,
grouping links, and review state. Split ranges at ownership boundaries while
retaining original patch relationships. Multiple associations do not duplicate
the changed material.

Scope controls semantic analysis, not existence in the inventory. Keep excluded
files, generated code, inactive build-tag sources, tests, and unsupported
languages discoverable with reasons and available diffs. Identify binary,
oversized, LFS, and unavailable content honestly.

Always expose unassociated changes, exclusions, and evidence gaps. Successful
grouping is not proof of complete analysis.

### Conservative correspondence

Analyze both sides independently. Existing symbol IDs and graph occurrence
IDs are revision-local, not cross-revision identity.

Match declarations using module/package identity, declaration kind, receiver,
and name; use retained source, Git path correspondence, and structural evidence
for supported moves and renames. Signature changes can modify a matched
declaration's contract. Closures and repeated calls need enclosing-owner and
call-site context, not line numbers or traversal serials.

Keep ambiguous correspondences explicitly unmatched or possible, with evidence.
Do not silently force a pairing. Recognizing formatting/movement does not prove
behavioral equivalence.

Entry correspondence uses entry kind and retained declaration/registration
provenance, including HTTP or WIT contract evidence, rather than handler identity
alone. Two routes or exports sharing a handler remain distinct entries.
Contract changes may be paired only when their provenance supports it;
unresolved entry correspondence stays explicit.

Pair occurrences separately from declarations. Preserve repeated calls,
asynchronous boundaries, changed conditions, arguments, results, targets,
and connections. Failed side analysis means comparison unavailable, not
mass deletion.

### Witnessed associations

Associate entries and changes using complete retained source/call/reference
evidence, not only visible Application nodes. Consult both sides to retain
deleted callers, entries, and relationships. Each association has a retrievable
entry-to-change witness and resolution quality.

Unresolved dispatch and unsupported cross-module/external relationships remain
limitations. A dependency relationship may justify potentially affected, not
a claim that behavior changed. Descriptions state observable source differences;
documentation intent remains distinct from implementation evidence.

### Collection and artifact ownership

Use tool-owned isolated revision directories without modifying the user's
checkout. Isolation is not a security sandbox. Never run branch tests,
generators, hooks, application binaries, or repository scripts by default.
Disclose Go-tool/dependency/export activity separately from target-test
execution permission. Do not silently resolve revision-local workspace/replace
paths to unrelated host files.

Use a common explicit analysis policy/environment, disclosing incompatibilities.
Metric comparisons retain scope and provenance. Missing coverage is unknown.
Incomparable test runs do not establish regressions, and changed member sets
can explain differences in maximum complexity.

Side reports and prepared views must never read the live checkout as a
substitute for retained revision inputs. Preserve comparison-owned inputs
needed for deferred analysis until replacement/closure. If the required
revision environment cannot be reproduced, report that limitation instead
of falling back to current source or a different dependency resolution.

Maintain one current comparison containing both sides, separately from current
Application evidence. Do not publish each side through the existing
latest-only repository-save path. Publish atomically; cancellation or failure
preserves the prior usable comparison. Explicitly incomplete partial module
results may be opened with missing analysis fully accounted for.

## 4. Agent and query integration

Chat is a full review interface. The UI and agent consume the same inventory,
correspondences, visible projections, annotations, reasons, and limitations.
Neither screenshots nor independent model-generated grouping establish facts.

Go owns collection and canonical immutable comparison evidence. Pure shared
projections produce review lists, flow views, comparisons, and details for
browser and backend queries, reusing existing revision-specific projections.
Avoid a parallel agent implementation or speculative generic framework.

Query overview and inventory summaries repository-wide, but hydrate detailed
side reports/projections for the requested module and scope. Do not load every
module's full Before/After graph or source into each query runtime or initial
browser render. Preserve deterministic ordering and paging across both clients.

### Capability parity

Every meaningful UI operation requires a structured agent equivalent:

- Resolve local refs or PR/MR inputs and propose collection.
- Read overview, inventory, filters, exclusions, evidence gaps, and progress.
- Search flows, changes, symbols, tests, and documents on either side.
- Retrieve why a flow is included, including its source witness.
- Query/select/expand/focus the same contextual flow.
- Compare source, conditions, contracts, calls, arguments/results, and matching
  evidence for selected changes.
- Inspect revision-specific logical/control/data/call evidence and readers.
- Inspect deleted, excluded, non-Go, and otherwise unassociated changes.
- Request review-state actions, undo, refresh, and cancellation through the
  same permission gates.

Parity is part of each operation's acceptance criteria, not a later feature.

### Comparison identity and citations

Do not overload the current single snapshotID. Explicit context identifies
comparison, module, Before/After snapshot, change, flow/occurrence, and focus
as applicable. Existing review_* snapshot behavior stays intact.

Source citations identify comparison, side, retained file/blob, and line range.
Deleted source opens Before, never the current checkout. Discovery supplies
usable IDs. Reject stale, foreign, or incompatible combinations on subsequent
queries/actions. Paired claims cite both sides; relationship claims can cite
their correspondence or association witness.

Preserve bounded discovery: compact summaries, explicit sections, paged detail,
counts, and continuation tokens. Do not silently truncate large source or
inventory results.

### Permission boundaries

- Evidence queries remain read-only, including public MCP.
- Navigation uses existing per-turn permission or an explicit Show action.
  Manual movement cancels late automatic navigation. Validate the entire target
  before applying one coherent history transition.
- Review-state mutations require an explicit request or confirmed action;
  navigation permission is insufficient.
- Collection, refresh, cancellation, and optional execution use shared
  host-owned commands and confirmation gates. Model arguments cannot authorize
  them. Disclose fetch/collection behavior; comparison creation does not execute
  tests.

Chat can initiate every workflow but cannot bypass safeguards. The public
evidence endpoint does not become an unauthenticated execution/mutation API.

If detailed static evidence requires preparation, expose the same preparation
request and status in both interfaces. A query must not secretly spawn analysis.
Prepared evidence belongs to a pinned side; changes in evidence identity
invalidate stale actions. Explain missing evidence rather than inventing it.

The current query inventory does not expose every detailed function mode,
and the existing data-flow HTTP handler reads the globally selected report.
Parity therefore requires explicit comparison-scoped query/action integration,
not simply exposing the current MCP registrations unchanged. Reuse detailed
analysis/projection logic, but never implement side selection by temporarily
swapping the server's selected Application report.

Cache pure derived views under immutable source/environment/tool/policy
identity. Caching a deterministic view of the same inputs does not replace
evidence or reset progress. Recollection with changed inputs or analysis
policy publishes a new evidence identity instead of mutating the old one.

### Representative interactions

- Why is this endpoint in the review? Retrieve the inclusion witness.
- Show what changed in its error path. Open the paired contextual evidence.
- Which other flows use this helper? Return supported associations and limits.
- What have I missed? Query complete inventory and persisted review state.

Explanations and citations remain useful with navigation permission off.

## 5. Progress, ownership, failure behavior, and acceptance

### Progress semantics

Track separately:

1. Unique changed items: Not reviewed, Reviewed, or Needs follow-up.
2. Affected flow context: explicitly considered or not considered.

Example: "18 of 27 changes reviewed; 3 of 6 flow contexts considered."
Do not collapse these into one percentage. Shared implementation is reviewed
once, but that does not establish that each caller's consequences were
considered.

Opening, scrolling past, expanding, or asking about an item never marks it
reviewed. Marking is explicit and reversible. Bulk actions identify their
exact item set and count before applying, including any items not currently
visible; they never silently include filtered or undisclosed work.
Needs follow-up remains unfinished.

Progress is local reviewer state, not provider approval, test success, or proof
of correctness. A reviewer can acknowledge an excluded/binary item after
inspecting available evidence, but its limitations remain visible.
If all items are marked reviewed, report unresolved analysis gaps separately;
never imply complete semantic analysis or certification.

Persist state outside the target checkout, bound to comparison evidence
identity. Reopening identical evidence restores progress. Changed revision
pairs, scope, tool/matching policy, or replaced evidence do not silently inherit
completion. Initial scope excludes fuzzy progress transfer across PR revisions.
Refresh explains the evidence-bound reset before replacing the current review.

### Concrete ownership

- Collection job owns ref resolution, temporary materialization, cancellation,
  side/module progress, and cleanup. An explicit owner waits for all work to
  terminate before releasing its resources.
- Comparison artifact owns immutable commits, inventories, side reports,
  correspondences, associations, provenance, limitations, and retained inputs
  needed for revision-specific analysis.
- Review state owns mutable progress with evidence identity and state version.
  Atomic persistence and conflict detection prevent two tabs or a late agent
  action from overwriting newer decisions.
- Browser navigation owns comparison/side/flow/selection/view state independently
  of ordinary Application selection.

Matching/grouping are deterministic transformations, not agent services.
Transport handlers translate requests rather than owning matching policy.
Shared rendering/query projections stay pure. Avoid interface-per-type,
generic plugin frameworks, and unrelated workbench refactoring.

Working data flow:

```text
Local refs or PR/MR
  -> resolved immutable revision request
  -> complete Git change inventory + independent side/module collection
  -> conservative correspondences + witnessed flow associations
  -> atomically published current comparison
       -> shared projection/query functions
            -> Change Review workspace
            -> bounded agent/MCP evidence queries
       -> evidence-bound local review state

UI or chat action
  -> host authorization + identity/state-version validation
  -> navigation, collection job, cancellation, or review-state transition
  -> visible success/failure + current state

Ordinary Application evidence/selection remains a separate current resource.
```

### Failure and cancellation

Expose phase, revision side, and module progress. Cancel works from UI and
chat. The last published comparison stays usable during replacement.
Failed or canceled work never reports success.

Distinguish unavailable Git objects, authorization/fetch failures, missing
dependencies, build incompatibility, partial semantic analysis, ambiguous
matching, and unsupported content. Explain available evidence and the smallest
useful recovery. A broken module cannot erase its changed files.

Retain one current comparison alongside current Application, not a history
of analysis runs. Clean only explicitly owned temporary inputs/artifacts;
never alter or prune the target checkout.

### Task-based acceptance

Each scenario must be demonstrated through both UI and chat:

1. Broad changes across many files: start with affected behaviors and drill
   into a changed error path without reading every diff.
2. Hidden helper changes with unchanged visible topology: discover the change
   and identify its supported callers.
3. Shared helper: review implementation once while each flow's consequences
   remain unconsidered until explicitly addressed.
4. Removed entry, deleted symbol, moved/renamed declaration, repeated call,
   closure, async path, ambiguous correspondence: preserve revision identity
   and honest matching.
5. Documentation, configuration, WIT, dependencies, tests, inactive build tags,
   binaries, and mode-only changes: account for every item, including changes
   without flow associations.
6. Side/module analysis failure: retain inventory and available source with
   no fabricated deletions or false completion.
7. Refresh, cancellation, reopening, two-tab state conflict, stale citations,
   and manual navigation during an agent turn: preserve identity and control.
8. Existing Application, detailed tools, readers, graph gestures, and Back:
   remain functional independently.

Hard checks:

- Every Git tree/text change has inventory representation, including changes
  without text hunks: 100% accounting.
- Every represented changed range is accounted for once; associations and
  occurrence counts do not duplicate unique review progress.
- Unsupported/inferred mappings remain distinguished from resolved ones.
- Stale or foreign-context actions applied: zero.
- Target tests/scripts executed by default: zero.
- User worktree modifications caused by collection: zero.
- Equivalent UI and agent requests return the same evidence, membership,
  annotations, and limitations.
- Every Change Review operation has an agent equivalent with the same
  authorization, error, and cancellation semantics.

Validate actual effective CSS viewport dimensions at 1920x1080, smaller desktop,
and narrow widths, plus browser zoom and accessibility behavior. Do not equate
physical screenshot size with usable CSS pixels.

Use representative reviewer tasks to compare navigation effort and mistakes
against the file-diff workflow. Record whether reviewers can identify the
changed behavior, explain its context, locate counterevidence, and account for
remaining work. Successful rendering and button counts are not usability proof.

## Repository evidence informing the design

- [endpoint-flow-tools.js](../../../internal/webui/endpoint-flow-tools.js): Application is a source-CFG
  projection, not an execution trace; it intentionally collapses helpers.
- [visual-review-tools.js](../../../internal/webui/visual-review-tools.js): hierarchy retains contextual
  occurrences; metrics use unique represented definitions, not occurrences.
- [symbols.go](../../../internal/review/symbols.go): symbol IDs include source offsets; they cannot
  serve as cross-revision semantic identity.
- [operation_sources.go](../../../internal/review/operation_sources.go): typed callable, argument, result,
  binding, and field-store evidence can support scoped comparisons.
- [query.go](../../../internal/webui/query.go): browser projections also execute in a restricted
  Goja runtime for backend queries; extend shared evidence rather than building
  a separate agent interpretation.
- [mcp.go](../../../internal/workbench/mcp.go): immutable report identities, stale-ID rejection,
  explicit detail discovery, and bounded paging are established patterns.
- [chat.go](../../../internal/workbench/chat.go) and [chat.js](../../../internal/webui/chat.js): structured
  navigation is validated twice and automatic navigation needs per-turn
  permission; late navigation is rejected after manual context changes.
- [server.go](../../../internal/workbench/server.go): the current server owns one selected report
  and repository review. Comparison sides must not be implemented by swapping
  that global selection.
- [current_review.go](../../../internal/review/current_review.go): current evidence publication prunes
  older repository runs. Comparison needs an explicit independent lifetime.
- [analyze.go](../../../internal/review/analyze.go): static analysis uses Go tooling and may resolve
  dependencies or build exports. Static does not mean no subprocess activity.
- [repository.go](../../../internal/review/repository.go): full repository review executes checks and
  tests. It is not the appropriate default comparison collection path.

## Design references

These sources inform the proposed interactions; they do not prescribe a
code-review product.

- Don Norman, [Signifiers, not affordances](https://jnd.org/signifiers-not-affordances/):
  make available actions and their meaning perceivable.
- Nielsen Norman Group, [Progressive disclosure](https://www.nngroup.com/articles/progressive-disclosure/):
  defer secondary detail without concealing the existence of pending work.
- Nielsen Norman Group, [Ten usability heuristics](https://www.nngroup.com/articles/ten-usability-heuristics/):
  visible state, recognition, consistency, user control, and actionable errors.
- Edward Tufte, [Time-series that move through XY space](https://www.edwardtufte.com/notebook/time-series-that-move-through-xy-space/):
  repeated comparative displays and the importance of showing relevant context.
- W3C, [Understanding use of color](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color.html):
  color cannot be the only signal conveying a difference or action.
