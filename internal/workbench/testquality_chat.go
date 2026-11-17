package workbench

import "encoding/json"

var qualityChatSchema = json.RawMessage(`{"type":"object","properties":{"assessmentID":{"type":"string"},"operation":{"type":"string"},"snapshotID":{"type":"string"},"symbolID":{"type":"string"},"fileID":{"type":"string"},"startLine":{"type":"integer"},"endLine":{"type":"integer"},"offset":{"type":"integer"},"limit":{"type":"integer"},"textOffset":{"type":"integer"},"textLimit":{"type":"integer"},"findingIndex":{"type":"integer"},"expectationID":{"type":"string"},"conditionID":{"type":"string"},"diagnosticIndex":{"type":"integer"},"runIndex":{"type":"integer"},"evidenceOperation":{"type":"string"},"args":{"type":"object"}},"required":["operation"]}`)

const testQualityAgentInstructions = `
TEST QUALITY
review_test_quality reads retained assessments, not live model calls or test execution.
Use operation assessments to discover IDs for the current project; overview shows rubric and
assessment scope, modules lists package groups, tests lists qualified tests.
events pages recorded agent activity with byte offsets; event reads one event at a
returned offset, paging the complete JSON text with textOffset/textLimit (runes).
Use these for actual assignments, tool calls/results and original errors. Older
assessments may explicitly report that history was not recorded.
Read test for rating rationale and findings, then source or evidence for tests,
helpers, assertions, coverage, CFG or data flow. Missing evidence is unknown, not zero.
Version 3: suite shows collective ratings; suite-groups pages agent group assessments;
expectations lists behaviors, expectation
and conditions use expectationID, condition also uses conditionID. suite-findings
pages collective findings. diagnostics lists records; diagnostic-runs and
diagnostic-artifacts use diagnosticIndex, diagnostic-attempts also uses runIndex.
Scores weight scored tests equally; table cases and subtests belong to their enclosing test.
Production functions and coverage are supporting context, never the assessment target.
Every applicable dimension must be assessed; insufficient/unreviewed scope remains explicit.
These are optional AI judgments separate from measured facts. Do not equate statement coverage
with assertion quality or invent individual test-to-path attribution from aggregate profiles.
Use navigate_test_quality only for requested movement or a useful proposal; it never starts work.
Cite saved test assessments as [test-quality:ASSESSMENT_ID|SNAPSHOT_ID|SYMBOL_ID].
Source details are available in that assessment's source/evidence operations, not ordinary
review_source. Never silently substitute the current checkout for pinned assessment evidence.
`
