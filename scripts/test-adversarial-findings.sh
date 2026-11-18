#!/bin/sh
# Runs focused regressions for the adversarial review findings.
set -eu
cd "$(dirname "$0")/.."
status=0
go test ./internal/review -run 'TestSystemFlowMergesInformation|TestFlowTypeNamePreservesAnonymous' -count=1 || status=1
go test ./internal/workbench -run 'TestAgentExploresRetainedModulesWithoutChangingSelection|TestQualityMCPReviewTestsDescriptionUsesInspectionAuthorization' -count=1 || status=1
node --test internal/webui/changes_ui_test.js internal/webui/system_flow_ui_test.js || status=1
exit "$status"
