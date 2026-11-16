#!/bin/sh
# Purpose: run focused regression checks for optional agent capabilities.
# Args: selector; provider-live URL MODEL probes local tool calling without project data;
# prompt-live URL MODEL [SCENARIO] evaluates investigation with synthetic retained evidence;
# live WORKBENCH_URL PROMPT [navigate] records actual retained-evidence chat events.
# embedding/retrieval selectors check configured-only semantic retrieval;
# embedding-live uses only configured embeddings and synthetic data;
# retrieval-build OUTPUT writes a verification binary to the specified path.
# quality selectors validate evidence-first facts, judgments, execution, archives and UI.
# quality-live [SCENARIO] checks advisory-result delivery with the configured local model on owned contracts,
# Requires GO_REVIEW_QUALITY_TRACE_DIR; uses production MCP/tools and request
# budgets for individual and collective assessment, not predetermined judgments.
# Stops at the first protocol/evidence failure.
# Each live case owns a five-minute context; the aggregate test timeout allows
# all seven cases to finish without changing those per-case execution bounds.
# quality-browser exports saved production-reader responses for read-only browser replay.
# quality-build OUTPUT checks dependent Go packages and writes a verification binary.
set -eu
cd "$(dirname "$0")/.."

selector=${1:-}
case "$selector" in
  quality-facts)
    go test ./internal/testquality -run 'TestQualityFacts|TestQualityExecutionFacts|TestStore|TestAggregate|TestQuery' -count=1
    ;;
  quality-performance)
    go test ./internal/testquality ./internal/workbench -run '^$' -bench 'BenchmarkQuality(Facts|Checkpoint|LargeSuite)' -benchmem -benchtime=1x -count=1
    ;;
  quality-browser)
    GO_REVIEW_QUALITY_BROWSER_ARCHIVE=1 go test ./internal/workbench -run '^TestQualityLiveBrowserArchive$' -count=1
    ;;
  quality-model)
    go test ./internal/testquality ./internal/workbench -run 'TestQuality(Suite|Judgment|Rubric|Batch)|TestStore|TestAggregate|TestQuery' -count=1
    ;;
  quality-execution)
    go test ./internal/testquality ./internal/review ./internal/workbench -run 'TestQualityDiagnostic|TestUnit|TestScope|TestSelected' -count=1
    ;;
  quality-workflow)
    go test -race ./internal/workbench ./internal/testquality -run 'TestQuality|TestQueue' -count=1
    ;;
  quality-ui)
    node --test internal/webui/test_quality_test.js internal/webui/assessment_integration_test.js internal/webui/retrieval_test.js internal/webui/agents_ui_test.js
    ;;
  quality-build)
    test "$#" -eq 2 || { echo "quality-build requires OUTPUT" >&2; exit 2; }
    go vet ./internal/testquality ./internal/workbench ./internal/review
    go build -o "$2" ./cmd/go-review
    ;;
  quality-live)
    scenario=${2:-}
    case "$scenario" in ""|combined|split|job|missing-behavior|missing-condition|logging-only|contracted-interaction|remaining) ;; *) echo "unknown quality-live fixture" >&2; exit 2;; esac
    pattern='^TestQualityLive$'
    if test "$scenario" = remaining; then pattern="$pattern/(split|missing-behavior|missing-condition|logging-only|contracted-interaction)$"
    elif test -n "$scenario"; then pattern="$pattern/$scenario"; fi
    GO_REVIEW_QUALITY_LIVE=1 go test ./internal/workbench -run "$pattern" -count=1 -v -timeout 40m
    ;;
  embedding-provider)
    go test ./internal/intelligence -run '^TestEmbedding' -count=1
    ;;
  retrieval-core)
    go test ./internal/retrieval -run '^TestChunks|^TestRank|^TestTrials' -count=1
    ;;
  retrieval-store)
    go test -race ./internal/retrieval -run '^TestEngine|^TestCache' -count=1
    ;;
  retrieval-workflow)
    go test -race ./internal/workbench ./internal/testquality -run '^TestRetrieval' -count=1
    ;;
  retrieval-quality)
    go test -race ./internal/testquality -count=1
    go test -race ./internal/workbench -run '^TestQuality' -count=1
    ;;
  retrieval-ui)
    node --test internal/webui/retrieval_test.js internal/webui/review_query_test.js internal/webui/agents_ui_test.js
    ;;
  retrieval-build)
    test "$#" -eq 2 || { echo "retrieval-build requires OUTPUT" >&2; exit 2; }
    go build -o "$2" ./cmd/go-review
    ;;
  embedding-live)
    GO_REVIEW_EMBEDDING_LIVE=1 go test ./internal/retrieval -run '^TestEmbeddingLive$' -count=1 -v
    ;;
  sources)
    go test ./internal/review -run '^TestAgentSources' -count=1
    ;;
  query)
    go test ./internal/evidence -run '^TestAgent' -count=1
    ;;
  provider)
    go test ./internal/intelligence -run '^TestAgentProvider|^TestAgentConfig' -count=1
    ;;
  provider-live)
    test "$#" -eq 3 || { echo "provider-live requires URL MODEL" >&2; exit 2; }
    GO_REVIEW_AGENT_URL="$2" GO_REVIEW_AGENT_MODEL="$3" go test ./internal/intelligence -run '^TestAgentProviderLive$' -count=1 -v
    ;;
  prompt-live)
    test "$#" -ge 3 && test "$#" -le 4 || { echo "prompt-live requires URL MODEL [SCENARIO]" >&2; exit 2; }
    GO_REVIEW_AGENT_URL="$2" GO_REVIEW_AGENT_MODEL="$3" go test ./internal/workbench -run "^TestAgentPromptLive/${4:-.*}$" -count=1 -v
    ;;
  chat)
    go test -race ./internal/workbench -run '^TestAgent' -count=1
    ;;
  live)
    test "$#" -ge 3 || { echo "live requires WORKBENCH_URL PROMPT [navigate]" >&2; exit 2; }
    shift
    node scripts/probe-review-chat.js "$@"
    ;;
  index)
    go test ./internal/intelligence -run '^TestAgentIndex|^TestAgentChunks' -count=1
    ;;
  transport)
    go test ./internal/workbench -run '^TestAgentHTTP|^TestAgentMCP' -count=1
    ;;
  opinions)
    go test ./internal/workbench -run '^TestAgentNavigation|^TestAgentMCPToolBoundaries' -count=1
    ;;
  workflow)
    go test ./internal/review ./internal/workbench ./cmd/go-review -run '^TestAgentWorkflow|^TestAgentCLI' -count=1
    ;;
  ui)
    node --test internal/webui/agents_ui_test.js internal/webui/parity_navigation_test.js
    ;;
  all)
    mkdir -p .work
    npm test
    go test -race ./... -count=1
    go vet ./...
    go build -o .work/go-review ./cmd/go-review
    ;;
  *)
    printf 'usage: %s {sources|query|provider|index|transport|opinions|workflow|chat|ui|all|quality-facts|quality-model|quality-performance|quality-execution|quality-workflow|quality-ui|quality-browser|embedding-provider|retrieval-core|retrieval-store|retrieval-workflow|retrieval-quality|retrieval-ui|embedding-live} or {quality-live [SCENARIO]} or {quality-build|retrieval-build OUTPUT} or {provider-live|prompt-live URL MODEL [SCENARIO]} or {live WORKBENCH_URL PROMPT [navigate]}\n' "$0" >&2
    exit 2
    ;;
esac
