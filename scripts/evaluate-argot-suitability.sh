#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK="$ROOT/.work/argot-suitability"
mkdir -p "$WORK/gotmp" "$WORK/results"
export GOTMPDIR="$WORK/gotmp" GOTOOLCHAIN=local GOWORK=off GOPROXY=off GOFLAGS=-mod=readonly
case "${1:-}" in
  build)
    cd "$WORK"
    go build -o "$WORK/analyzer" .
    ;;
  snapshot)
    : "${ARGOT_RUN_ID:?set ARGOT_RUN_ID}"
    mkdir -p "$WORK/results/$ARGOT_RUN_ID"
    python3 "$WORK/snapshot.py" "$ROOT/.work/flow-reuse-evaluation" "$WORK/fixture" "/Users/russell.brasser/projects/builder/server" "/Users/russell.brasser/projects/builder/components/workflow-handler" > "$WORK/results/$ARGOT_RUN_ID/$2.snapshot"
    ;;
  run)
    name=$2; dir=$3; mode=$4; shift 4
    : "${ARGOT_RUN_ID:?set ARGOT_RUN_ID}"
    mkdir -p "$WORK/results/$ARGOT_RUN_ID"
    cancel=0
    [ "$mode" != cancel ] || { cancel=1; mode=real; }
    if [ "$mode" = realwasm ]; then
      export GOOS=wasip1 GOARCH=wasm GOFLAGS="-mod=readonly -tags=componentizego_async"
      mode=real
    fi
    cd "$dir"
    python3 "$WORK/watch.py" 180 1800000 "$cancel" "$WORK/analyzer" "$dir" "$mode" "$@" > "$WORK/results/$ARGOT_RUN_ID/$name.log" 2>&1
    ;;
  *)
    echo "usage: $0 build | snapshot LABEL | run NAME DIR MODE PATTERN..." >&2
    exit 2
    ;;
esac
