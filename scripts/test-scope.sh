#!/bin/sh
# Verifies saved review scope, excluded evidence and execution against local fixtures.
# Optional argument all also runs frontend tests, Go race tests, vet and build.
set -eu
cd "$(dirname "$0")/.."
go test ./internal/review ./internal/workbench -run 'TestScope' -count=1
node --test internal/webui/repository_ui_test.js
if [ "${1:-}" = all ]; then
  npm test
  go test -race ./... -count=1
  go vet ./...
  go build -o .work/go-review ./cmd/go-review
fi
