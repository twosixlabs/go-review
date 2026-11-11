#!/bin/sh
# Purpose: run focused regression checks for optional agent capabilities.
# Args: selector; provider-live URL MODEL probes local tool calling without project data;
# prompt-live URL MODEL [SCENARIO] evaluates investigation with synthetic retained evidence;
# live WORKBENCH_URL PROMPT [navigate] records actual retained-evidence chat events.
set -eu
cd "$(dirname "$0")/.."

selector=${1:-}
case "$selector" in
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
    printf 'usage: %s {sources|query|provider|index|transport|opinions|workflow|chat|ui|all} or {provider-live|prompt-live URL MODEL [SCENARIO]} or {live WORKBENCH_URL PROMPT [navigate]}\n' "$0" >&2
    exit 2
    ;;
esac
