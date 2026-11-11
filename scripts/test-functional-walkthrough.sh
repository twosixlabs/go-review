#!/bin/sh
# Verifies source-linked functional operations with controlled fixtures.
# Args: sources|system|branches|execution|runtime|api|ui|frontend|build|backend|all;
# or builder|wasm MODULE_DIR EVIDENCE_DIR for opt-in real-repository acceptance;
# builder-ui EVIDENCE_DIR reopens it; endpoint-ui SNAPSHOT checks retained entry flows;
# endpoint-live-ui URL checks the running workbench assets and snapshot in jsdom;
# visual-fixtures EVIDENCE_DIR collects unrelated service/CLI/library snapshots
# and checks the integrated visual workspace without executing fixture code;
# entries checks static entry-point collection and shared UI/MCP disclosure;
# mcp checks shared query and MCP transport; mcp-build builds server/client;
# mcp-live URL compares MCP with UI projections; preview renders SVGs with Quick Look.
set -eu
cd "$(dirname "$0")/.."
case "${1:-sources}" in
  entries)
    go test ./internal/review -run '^TestEntryPoints|^TestWasmEntry|^TestWIT' -count=1
    node --test internal/webui/application_tools_test.js internal/webui/endpoint_flow_test.js internal/webui/review_query_test.js
    go test ./internal/webui ./internal/workbench -run '^TestReviewQuery|^TestMCP|^TestAgentNavigation' -count=1
    ;;
  mcp)
    node --test internal/webui/review_query_test.js internal/webui/visual_review_test.js
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
  *) echo "usage: sh scripts/test-functional-walkthrough.sh [entries|sources|system|branches|execution|runtime|api|ui|frontend|build|backend|mcp|mcp-build|all] or [builder|wasm MODULE_DIR EVIDENCE_DIR] or [builder-ui|preview|visual-fixtures EVIDENCE_DIR] or [endpoint-ui SNAPSHOT] or [endpoint-live-ui URL] or [mcp-live MCP_URL]" >&2; exit 2 ;;
esac
