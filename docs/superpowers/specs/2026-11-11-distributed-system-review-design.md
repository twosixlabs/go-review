# Distributed system review

## Approved relationship map

```text
System workspace
  ├─ Project identities and pinned, retained project snapshots
  ├─ User-specified infrastructure, connections and corrections
  └─ System snapshot and relationship projection
       ├─ Source, entry, boundary, operation and data-flow evidence
       ├─ Available contracts, configuration, test and coverage evidence
       ├─ System diagram and project-level drill-down
       ├─ Single-project change-impact exploration
       └─ Chat and MCP navigation over the same retained evidence
```

The workspace owns system membership and user knowledge. Existing project
reviews own their evidence. The projection combines these inputs; it does not
replace project reports or require a distributed runtime trace. The diagram,
change-impact view, chat and MCP consume the same system snapshot and its
relationships. Proposed data structures and interfaces below are working
hypotheses to validate against functional implementation, not fixed APIs.

## Purpose and scope

Provide a distributed-system counterpart to the Application view. An engineer
selects projects known to work together and uses a high-level map to understand
the major applications, shared infrastructure and general information
exchanges. The same relationships help identify other components worth
examining when one project changes.

The goal is architectural understanding, not tracing every operation through
clients, libraries and infrastructure. Make useful inferences from the evidence
that exists, display them automatically, and allow the engineer to navigate to
the underlying application and change evidence.

The initial design is Go-first and local. It covers inferred HTTP, messaging,
shared database and contract relationships wherever retained evidence supports
them, as well as user-specified connections across those and other mechanisms.
The precise libraries, routers, message systems and database patterns for which
automatic extraction works must be established using representative projects
and tested fixtures; a generic external boundary alone does not guarantee that
the topic, route or database identity is available. The feature must still
show useful coarse relationships or unresolved boundaries where detail is
missing.

Non-goals are exhaustive data lineage, a runtime sequence, proof of individual
message delivery or database reads, automatic certification of compatibility or
breakage, coordinated changes across multiple projects, and a dependency on AI,
a cloud service, a Git hosting provider or a running distributed deployment.
Adding language support is a separate effort.

## Workspace and snapshots

A system workspace is a durable local object with a name, stable project and
infrastructure identities, user knowledge and presentation preferences. A
project can represent a repository containing multiple reviewed Go modules.
The user selects existing retained reviews or explicitly starts reviews through
the existing collection workflow. System membership expresses the user's
knowledge that projects belong together; it does not imply every pair
communicates.

A system snapshot pins, per project, the selected retained module reports,
source and revision identities, collection scope and status. It also retains
the relevant version of the user's declarations and corrections, the derived
relationships, their evidence and the inference version. Snapshot-qualified
references distinguish identical source or symbol IDs in different projects
and revisions. A dirty source snapshot remains identified by its retained
source identity rather than being represented as a clean commit. The user can
inspect and reopen a snapshot without accessing live source or rerunning
collection.

The workspace owns project membership, declarations, suppressions and layout;
individual snapshots are immutable records of the inputs and derived results
used for a review. Presentation changes may be saved independently of evidence.
An explicit refresh selects new project reports and produces a new system
snapshot; it does not overwrite historical ones. User declarations persist
unless the user edits or removes them. References that cannot be carried
forward are marked unresolved or stale and remain inspectable. Do not silently
attach a source link or a rejected suggestion to a different entity based only
on a similar name.

Use the existing local artifact storage approach, with versioned records,
validated snapshot references and atomic publication so an interrupted update
does not replace a usable snapshot with a partial one. Missing, moved, corrupt
or incompatible project evidence is reported with the affected project and
remediation; other available projects remain navigable. A collection or
projection failure must not look like an empty successful analysis.

## Relationship inference

The projection builds a system-level view over existing per-project evidence:

- Application components, entry points, source-backed external boundaries,
  calls, operation arguments, types, wiring and source locations.
- Retained intraprocedural data-flow graphs where available, including
  source-visible constants, parameters and operands at calls. These support
  useful local inferences; they are not treated as interprocedural or
  cross-project value traces.
- Available API or message contracts, deployment and project configuration,
  test and mock source, and coverage or runtime observations with their
  original attribution.
- User declarations about projects, shared infrastructure and connections,
  including known identities, direction, routes, subjects, tables or
  contracts when supplied.

Interpret recognizable operations at their external boundaries using the
relevant mechanism's semantics, without tracing through library internals.
Examples include matching HTTP methods and compatible route patterns; matching
publishers and subscribers on compatible subjects on a shared bus; and
associating writes and reads of a shared database table or column. Known
contracts and types, configuration, mock expectations and tests may reinforce
or help discover a relationship. A user declaration that two projects share a
bus or database is valid context for matching even when the runtime address is
not in the source.

Represent infrastructure as a first-class node where it explains the
relationship, such as a message bus with multiple consumers or a database with
multiple readers and writers. A direct project-to-project relationship is
appropriate when that is the useful abstraction, for example an HTTP request.
Group implementation details into concise exchanges such as “publishes order
events” or “reads order status,” with drill-down to contributing operations;
do not make each call or SQL statement a separate required diagram node.

Each relationship preserves its endpoints, direction if known, mechanism,
information or contract where known, source snapshot-qualified evidence, and
its basis: user-specified, inferred from retained evidence, or observed in a
specified execution. A relationship may have several bases. Merge additional
evidence into the same logical relationship where identity is established;
keep multiple plausible matches visible rather than choosing an arbitrary one.
Do not treat matching test fixtures or mocks as production observations, or
coverage as an assertion about argument values or successful delivery.

User-specified connections are trusted working knowledge. Missing supporting
evidence does not invalidate them. If **specific** evidence contradicts a
declaration, display the conflict and both sources, retaining the declaration
until the user resolves it. Users can add, edit or remove declarations and
accept, correct or dismiss inferred candidates. Corrections persist with their
scope and provenance, and should inform future matching without silently
inventing specific operation-level connections from a broad declaration.

The overview shows automatically suggested relationships immediately, along
with declared relationships; it does not require the user to confirm each
suggestion before it is useful. Ambiguous candidates, unmatched input/output
boundaries, missing capabilities and relevant limitations remain available
without overwhelming the main map. Distinguish shared access to a resource from
an assertion that a particular read returned a particular write. Show useful
inference as inference, not as a failure to prove a runtime trace.

## Interaction and navigation

The desktop system view follows the Application view's emphasis on logical
behavior and meaningful information exchanges, not package layout or
call-by-call chronology. Users can:

1. Create a system workspace, choose retained project reviews and identify
   shared infrastructure or known relationships as needed.
2. Explore the automatically populated overview, filter or focus on a project,
   resource or mechanism, and expand grouped relationships.
3. Draw a connection between projects or infrastructure and supply the
   information they know; an unspecified topic or route is allowed.
4. Select an exchange to see its direction, information, contributing
   evidence, declaration or inference basis, unresolved details and any
   specific conflicts.
5. Navigate to the associated project Application view, source, test, contract
   or existing Change Review while preserving system context and a return path.

Drill-down uses retained, snapshot-qualified evidence rather than the live
checkout. State clearly when a source, module, test or graph is unavailable in
the selected snapshot. The user can review detailed evidence but should not be
required to resolve every candidate to obtain a useful system overview.

## Single-project change impact

Starting from a system snapshot and an existing Git-only Change Review for one
member project, use the comparison's retained before and after reports and
change records. Build before and after relationship projections with the
changed project on the corresponding comparison side and every other project
on its pinned baseline snapshot. The workspace identifies the changed project,
comparison, revisions, module mapping and side explicitly; it must not compare
against silently refreshed peers.

Map changed source, declarations, flows and contracts to boundary evidence
where the retained comparison supports that association. Highlight potentially
added, removed or affected exchanges and related peers, including consumers of
shared messages or database data. Let the user follow a highlighted
relationship to its supporting evidence, the changed responsibility and
before/after source, and the peer's Application view. Where precise mapping is
unavailable, offer the coarser affected project or boundary and show the
unresolved mapping rather than inventing a precise impact.

An impact is a review lead, not a claim that the peer breaks or that a
particular execution happened. A user-declared relationship remains a usable
impact path even if automatic source matching is unavailable. Handle missing or
partial before/after module evidence, added or removed modules, stale links and
unavailable peer reports explicitly. The initial workflow compares one changed
project at a time; it does not claim to model a coordinated multi-project
release or compatible deployments.

## Chat and MCP

Chat and MCP use system-snapshot-qualified queries over the same projection
shown in the UI. They can enumerate projects, infrastructure and relationships,
explain a connection with its contributing evidence, navigate to project
evidence, and explore single-project change-impact paths. Return explicit
status and provenance for declared, inferred and observed evidence, ambiguity,
conflicts and unavailable snapshots. Do not let assistant-generated
interpretations become authoritative system evidence. Existing project-scoped
tools and single-project workflows remain available.

## Collection, trust and privacy

Creating or browsing a system view from retained artifacts performs no target
execution and does not implicitly refresh reviews. New project reviews and
test execution continue to require the existing explicit authorization and
scope. Repository checks and test code are trusted code, not sandboxed. The
feature does not require connecting to a live message bus, database or
deployment, collecting credentials, sending project data to an external
service, or obtaining GitHub/GitLab access. Optional chat uses the existing
user-selected model configuration; core inference and navigation work without
AI. Stored configuration evidence must not expose secrets in the UI or
assistant context.

## Validation and acceptance

Use realistic retained multi-project fixtures to evaluate behavior end to end,
not only isolated matcher functions:

- An HTTP client and server, publisher and subscriber on a shared bus, and a
  writer and reader of a shared database produce useful suggested exchanges
  with navigable source evidence, without library-level tracing.
- Data-flow operands, contracts and mock/test evidence contribute where
  available; a test-only example is attributed to the test and coverage is not
  promoted into a value observation.
- User-specified links appear without confirmation, improve matching, persist
  across refresh, and remain present when support is absent. A specific
  contradiction is visible and does not silently erase the declaration.
- Fan-out, multiple matching candidates, unmatched boundaries and same-named
  resources in distinct known contexts remain distinguishable.
- Reloading a pinned system snapshot is deterministic and does not run
  collection; explicit refresh preserves the earlier snapshot and reports
  missing, stale or partial evidence without losing unrelated results.
- A single-project before/after comparison highlights relevant peers against
  pinned snapshots, handles missing or added modules and preserves the
  distinction between a review lead and proven breakage.
- UI navigation, chat and MCP return the same relationships and snapshot
  provenance; existing single-project review and Git-only Change Review remain
  functional.

Fixtures should include cases that must not match as well as expected matches,
and results on representative real projects should inform which additional
boundary extraction rules are necessary. Manual and automated evidence should
remain useful when some collectors are unsupported or incomplete.
