#!/bin/sh
# Verifies review-progress reconnection, polling, and API state.
# Optional parameter: all also runs the full suites, vet, and production build.
set -eu
cd "$(dirname "$0")/.."
node --test internal/webui/repository_ui_test.js
go test ./internal/workbench ./internal/review -run 'TestReview|TestRepositoryReview|TestCanceled|TestOneReviewRequest' -count=1
if [ "${1:-}" = all ]; then
  npm test
  go test -race ./... -count=1
  go vet ./...
  go build -o .work/go-review ./cmd/go-review
fi
