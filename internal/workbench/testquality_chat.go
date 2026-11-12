package workbench

import "encoding/json"

var qualityChatSchema = json.RawMessage(`{"type":"object","properties":{"assessmentID":{"type":"string"},"operation":{"type":"string"},"snapshotID":{"type":"string"},"symbolID":{"type":"string"},"fileID":{"type":"string"},"startLine":{"type":"integer"},"endLine":{"type":"integer"},"offset":{"type":"integer"},"limit":{"type":"integer"},"findingIndex":{"type":"integer"},"evidenceOperation":{"type":"string"},"args":{"type":"object"}},"required":["operation"]}`)

const testQualityAgentInstructions = `
TEST QUALITY
review_test_quality reads retained assessments, not live model calls or test execution.
Use operation assessments to discover IDs for the current project; overview shows rubric and
coverage of scope, modules lists package groups, responsibilities lists qualified functions.
Read responsibility for rating rationale and findings, then source or evidence for tests,
helpers, assertions, coverage, CFG or data flow. Missing evidence is unknown, not zero.
Scores weight scored production functions equally; closures belong to their enclosing function.
Every applicable dimension must be assessed; insufficient/unreviewed scope remains explicit.
These are optional AI judgments separate from measured facts. Do not equate statement coverage
with assertion quality or invent individual test-to-path attribution from aggregate profiles.
Use navigate_test_quality only for requested movement or a useful proposal; it never starts work.
Cite saved function assessments as [test-quality:ASSESSMENT_ID|SNAPSHOT_ID|SYMBOL_ID].
Source details are available in that assessment's source/evidence operations, not ordinary
review_source. Never silently substitute the current checkout for pinned assessment evidence.
`
