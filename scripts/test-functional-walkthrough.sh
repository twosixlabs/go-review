#!/bin/sh
# Verifies source-linked functional operations with controlled fixtures.
# Args: changes|changes-ui|sources|system|branches|execution|runtime|dataflow|api|ui|frontend|build|backend|all;
# changes-fixture NEW_ABSOLUTE_DIR retains a controlled Git fixture for real-browser review;
# changes-portability cross-builds comparison collection/progress for Windows and Linux;
# changes-acceptance exercises real Git collection through integrated UI and controlled chat;
# or builder|wasm MODULE_DIR EVIDENCE_DIR for opt-in real-repository acceptance;
# builder-ui EVIDENCE_DIR reopens it; endpoint-ui SNAPSHOT checks retained entry flows;
# endpoint-live-ui URL checks the running workbench assets and snapshot in jsdom;
# visual-fixtures EVIDENCE_DIR collects unrelated service/CLI/library snapshots
# and checks the integrated visual workspace without executing fixture code;
# entries checks static entry-point collection and shared UI/MCP disclosure;
# wasm-review checks component review guards and dependency flow regressions;
# current-review checks latest-only storage and review UI; current-storage,
# current-api and current-ui isolate its storage, API and UI regressions;
# workspace checks structural layout, contextual navigation and Application regressions;
# mcp checks shared query and MCP transport; mcp-build builds server/client;
# mcp-live URL compares MCP with UI projections; preview renders SVGs with Quick Look.
set -eu
cd "$(dirname "$0")/.."
case "${1:-sources}" in
  changes-fixture)
    test "$#" -eq 2 || { echo "changes-fixture requires a new absolute fixture directory" >&2; exit 2; }
    GO_REVIEW_CHANGE_FIXTURE_DIR="$2" go test ./internal/workbench -run '^TestChangeInteractiveAcceptance$' -count=1
    ;;
  changes-portability)
    GOOS=windows GOARCH=amd64 go build ./internal/changes
    GOOS=linux GOARCH=amd64 go build ./internal/changes
    ;;
  changes-acceptance)
    go test ./internal/workbench -run '^TestChange.*InteractiveAcceptance$' -count=1 -v
    ;;
  changes)
    go test ./internal/changes ./internal/workbench -run '^TestChange' -count=1
    node --test internal/webui/change*_test.js
    ;;
  changes-ui)
    node --test internal/webui/change*_test.js
    ;;
  dataflow)
    go test ./internal/review ./internal/workbench -run '^TestDataFlow' -count=1
    node --test internal/webui/dataflow_test.js
    ;;
  workspace)
    node --test internal/webui/workspace_ui_test.js internal/webui/endpoint_flow_test.js internal/webui/repository_ui_test.js
    ;;
  current-storage)
    go test ./internal/review -run '^TestCurrentReview|^TestReviewReplacement|^TestRepositoryReviewSkipped' -count=1
    ;;
  current-ui)
    node --test internal/webui/repository_ui_test.js
    ;;
  current-api)
    go test ./internal/workbench -run '^TestReviewReplacement|^TestReviewEndpointRequires|^TestUnitEvidenceRefresh' -count=1
    ;;
  current-review)
    go test ./internal/review ./internal/workbench -run '^TestCurrentReview|^TestReviewReplacement|^TestRepositoryReviewSkipped|^TestUnitEvidenceRefresh' -count=1
    node --test internal/webui/repository_ui_test.js
    ;;
  wasm-review)
    go test ./internal/review -run '^TestOperationSourcePackageOrigin|^TestReachabilityDoesNotTreatWIT|^TestFlowExecutionRejectsNativeWasm|^TestFlowExecutionAllowsHostCompatibleWasm|^TestRepositoryReviewReportsWasm' -count=1
    node --test internal/webui/endpoint_flow_test.js internal/webui/review_query_test.js
    ;;
  entries)
    go test ./internal/review -run '^TestEntryPoints|^TestWasmEntry|^TestWIT' -count=1
    node --test internal/webui/application_tools_test.js internal/webui/endpoint_flow_test.js internal/webui/review_query_test.js
    go test ./internal/webui ./internal/workbench -run '^TestReviewQuery|^TestMCP|^TestAgentNavigation' -count=1
    ;;
  mcp)
    node --test internal/webui/review_query_test.js internal/webui/parity_query_test.js internal/webui/visual_review_test.js
    go test ./internal/webui ./internal/workbench -run 'TestReviewQuery|TestMCP' -count=1
    ;;
  mcp-build)
    mkdir -p .work
    go build -o .work/go-review-mcp-client ./cmd/go-review-mcp-client
    go build -o .work/go-review-functional-check ./cmd/go-review
    ;;
  mcp-live)
    test "$#" -eq 2 || { echo "mcp-live requires MCP_URL" >&2; exit 2; }
    GO_REVIEW_MCP_URL="$2" node --test internal/webui/mcp_acceptance_test.js
    ;;
  sources) go test ./internal/review -run '^TestOperationSource' -count=1 ;;
  system) go test ./internal/review -run '^TestSystemFlow' -count=1 ;;
  branches) go test ./internal/review -run '^TestFlowCoverage' -count=1 ;;
  execution) go test ./internal/review -run '^TestFlowExecution' -count=1 ;;
  runtime) go test ./internal/review -run '^TestFlowRuntime' -count=1 ;;
  api) go test ./internal/workbench -run '^TestOperationAPI|^TestUnitEvidence' -count=1 ;;
  ui) node --test internal/webui/graph_test.js internal/webui/system_flow_tools_test.js internal/webui/system_flow_ui_test.js internal/webui/unit_evidence_ui_test.js internal/webui/endpoint_flow_test.js internal/webui/visual_review_test.js ;;
  frontend) npm test ;;
  build)
    mkdir -p .work
    go build -o .work/go-review-functional-check ./cmd/go-review
    ;;
  builder)
    test "$#" -eq 3 || { echo "builder requires MODULE_DIR EVIDENCE_DIR" >&2; exit 2; }
    GO_REVIEW_BUILDER_DIR="$2" GO_REVIEW_ACCEPTANCE_DIR="$3" go test ./internal/workbench -run '^TestSystemFlowBuilderAcceptance$' -count=1 -v
    GO_REVIEW_ACCEPTANCE_DIR="$3" node --test internal/webui/system_flow_acceptance_ui_test.js
    ;;
  wasm)
    test "$#" -eq 3 || { echo "wasm requires MODULE_DIR EVIDENCE_DIR" >&2; exit 2; }
    GO_REVIEW_WASM_DIR="$2" GO_REVIEW_ACCEPTANCE_DIR="$3" go test ./internal/workbench -run '^TestSystemFlowWasmAcceptance$' -count=1 -v
    ;;
  builder-ui)
    test "$#" -eq 2 || { echo "builder-ui requires EVIDENCE_DIR" >&2; exit 2; }
    GO_REVIEW_ACCEPTANCE_DIR="$2" node --test internal/webui/system_flow_acceptance_ui_test.js
    ;;
  endpoint-ui)
    test "$#" -eq 2 || { echo "endpoint-ui requires SNAPSHOT" >&2; exit 2; }
    GO_REVIEW_ENDPOINT_SNAPSHOT="$2" node --test internal/webui/endpoint_flow_acceptance_test.js
    ;;
  endpoint-live-ui)
    test "$#" -eq 2 || { echo "endpoint-live-ui requires URL" >&2; exit 2; }
    GO_REVIEW_ENDPOINT_URL="$2" node --test internal/webui/endpoint_flow_acceptance_test.js
    ;;
  visual-fixtures)
    test "$#" -eq 2 || { echo "visual-fixtures requires EVIDENCE_DIR" >&2; exit 2; }
    GO_REVIEW_VISUAL_FIXTURES="$2" go test ./internal/workbench -run '^TestVisualReviewProjects$' -count=1
    GO_REVIEW_VISUAL_FIXTURES="$2" node --test internal/webui/endpoint_flow_acceptance_test.js
    ;;
  preview)
    test "$#" -eq 2 || { echo "preview requires EVIDENCE_DIR" >&2; exit 2; }
    command -v qlmanage >/dev/null || { echo "preview requires macOS Quick Look" >&2; exit 1; }
    sh "$0" builder-ui "$2"
    qlmanage -t -s 1600 -o "$2" "$2/builder-context.svg" "$2/builder-start.svg"
    ;;
  backend) go test ./internal/review ./internal/workbench -count=1 ;;
  all)
    npm test
    go test -race ./... -count=1
    go vet ./...
    mkdir -p .work
    go build -o .work/go-review-functional-check ./cmd/go-review
    ;;
  *) echo "usage: sh scripts/test-functional-walkthrough.sh [changes|changes-ui|changes-acceptance|changes-portability|workspace|current-review|current-storage|current-api|current-ui|wasm-review|entries|sources|system|branches|execution|runtime|api|ui|frontend|build|backend|mcp|mcp-build|all] or [changes-fixture NEW_ABSOLUTE_DIR] or [builder|wasm MODULE_DIR EVIDENCE_DIR] or [builder-ui|preview|visual-fixtures EVIDENCE_DIR] or [endpoint-ui SNAPSHOT] or [endpoint-live-ui URL] or [mcp-live MCP_URL]" >&2; exit 2 ;;
esac
