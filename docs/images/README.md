# Screenshot examples

These are actual browser captures of Go Review, not mockups, generated
illustrations or fabricated model responses. Open a PNG link below to view it
outside the main guide. No application source or results were altered for a
capture.

## Builder: retained application review

The Builder examples use an existing repository review, reopened with
`go-review open /path/to/retained/review.json`. Builder's tests and verification
scripts were **not rerun to make these screenshots**.

The selected module is `builder` (the repository's `server` module), with
38 packages, 3,322 symbols and 37 discovered HTTP entries. Its retained source
fingerprint is:

```text
c13f7651ef686d59e77ac24ca3948018b85ba0df27386fc92a928e976da1448f
```

This is the collector's source identity, **not a Git commit ID**. The module
overview has 91.9% retained statement coverage and maximum cyclomatic complexity
43. Function examples use `internal/server/httpapi · server.getDraft`
(`GET /api/builds/{buildID}/draft`), with 88.9% retained coverage and complexity 6.
These measurements describe that snapshot, not the latest Builder checkout.

| Screenshot | Interaction shown |
|---|---|
| [Application](application-builder.png) | Select the server module and open Application |
| [Application flow](application-flow-builder.png) | Select the draft GET entry; inspect Draft and error alternatives |
| [Logical flow](logical-flow-builder.png) | Search `getDraft`, select the function, and open Logical flow |
| [Control flow](control-flow-builder.png) | Switch that function to Control flow; inspect the Draft call and error decision |
| [Data flow](data-flow-builder.png) | Select the `PathValue` call, choose Direct dependencies & uses, then Fit |
| [Call relationships](call-relationships-builder.png) | Switch the function to Call relationships |
| [System context](system-context-builder.png) | Open System context and Fit the broad boundary diagram |
| [Components](components-builder.png) | Select the `internal/app/assist/build` group, Fit, then select Builder |
| [Packages](packages-builder.png) | Open Packages at a readable scale; the rest of the map is pannable |
| [Review map](review-map-builder.png) | Open Review map; compare known and unknown coverage against complexity |
| [Architecture signals](architecture-signals-builder.png) | Open the overview of rule findings and package concern |
| [Checks & hygiene](checks-builder.png) | Open the expandable check outcomes |
| [Documentation](documentation-builder.png) | Open the retained project README in the wide reader |
| [Review results](review-results-builder.png) | Scroll the wide reader to per-module stage outcomes |
| [Assistant conversation](assistant-builder-conversation.png) | Open the draft entry, then ask the assistant to explain its steps and error handling |
| [Assistant evidence activity](assistant-builder-evidence.png) | Expand **Explored evidence**, then **Reading source** for that response |

The component module in the same repository has two explicitly not-run stages;
this is why the repository banner and selected server's checks have different
counts. Unknown coverage is not measured zero. Source-derived registration,
call, control, import, contract and SSA arrows do not have interchangeable
meanings.

The assistant screenshots show a real conversation using the retained Builder
review and the locally running `ornith-1.5-35b-a3b-mlx` model. The activity
shows the source range requested by the assistant; its answer is model output
and should be checked against the retained evidence.

### Builder close-ups

These are crops of actual desktop captures; no widget or evidence was redrawn.

| Close-up | Source and interaction |
|---|---|
| [Flow controls](closeup-flow-controls.png) | Crop of [assistant evidence activity](assistant-builder-evidence.png), showing Draft and its error alternative |
| [Chat evidence](closeup-chat-evidence.png) | Crop of [assistant evidence activity](assistant-builder-evidence.png), showing its expanded source request |
| [Review map filters](closeup-review-map-filters.png) | Fresh desktop capture of Builder's retained review map, cropped to its filter panel |
| [Unknown coverage](closeup-unknown-coverage.png) | Crop of the same review map, showing its zero line and unknown band |

## Go Review: committed changes

These screenshots compare actual commits in this repository:

- Before: `0e4ba08a059de8e9abdffec876c91511f034fdbb`
- After: `22fa986f14fb3c6a6033b5b066490cadb79bff9a`

The comparison was explicitly resolved and collected through Change Review
before the revision input controls were changed to the Git-only workflow.
It used independent static analysis of each revision; target tests and scripts
were not executed. The current uncommitted documentation changes are not part
of that comparison.

| Screenshot | Interaction shown |
|---|---|
| [Affected flows](change-review-go-review.png) | Select `/api/chat/status` and Fit flow |
| [Highlighted side-by-side diff](change-diff-go-review.png) | Select `/api/chat/status`, open `Server.chatStatus`, compare retained file evidence and scroll to its changed lines |
| [Earlier paired source reader](change-comparison-go-review.png) | Historical capture of the `Server.chatStatus` responsibility before the highlighted diff reader |

The diff screenshot uses the current workspace frontend assets loaded directly
in a browser against the existing local workbench and its retained comparison.
The browser's content security policy was bypassed for that capture to load
those local assets; the retained comparison and source responses were not
modified. The capture did not restart the workbench or rerun collection.

The [revision inputs](closeup-change-revisions.png), [remote and merge-base
controls](closeup-change-remote.png), and [responsibility actions](closeup-change-actions.png)
are crops of a fresh desktop capture using the same browser and retained
comparison. The example revision fields were filled for illustration, but were
not resolved or collected again.

The comparison has 347 unique changed items and 13 affected flow contexts.
No completion marks were applied for the screenshots. These counts are evidence
inventory and local progress, not an approval score.

## Capture notes

Captures use the local workbench, a dark desktop browser and a measured
1920 × 1080 CSS-pixel viewport. Browser capture scaling produces 1142 × 642 PNGs
for the original guide captures and 1862 × 1048 PNGs for the assistant and diff captures;
those file dimensions are not the CSS layout dimensions. No narrow-screen
layout was introduced. Large diagrams remain at a readable working scale or
are explicitly fitted, rather than pretending every graph label can be read
at once.

The images retain real source paths, package names, source excerpts and snapshot
measurements. They do not contain provider credentials or fabricated assistant
answers. Local absolute paths identify the capture machine, not paths readers
must reproduce. Different projects, scope policies, revisions and collection
results will naturally show different entries, measurements and diagrams.
