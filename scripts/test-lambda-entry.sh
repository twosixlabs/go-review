#!/bin/sh
# Run Lambda entry-point regressions, or the full suites with --full.
set -eu
cd "$(dirname "$0")/.."
if [ "${1:-}" = "--full" ]; then
  go test ./...
  npm test
  exit
fi
go test ./internal/review -run 'TestLambdaEntry' -count=1
node --test internal/webui/application_tools_test.js internal/webui/application_test.js \
  internal/webui/walkthrough_test.js internal/webui/endpoint_flow_test.js
