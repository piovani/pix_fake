#!/usr/bin/env bash
# Starts the wallet's API and worker and the simulator on http://localhost:8090, against the environment of make up.
# Ctrl-C stops the three.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$(mktemp -d)"
pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; rm -rf "$BIN"; }
trap cleanup EXIT INT TERM

if ! curl -fs localhost:8081/healthz >/dev/null 2>&1; then
  echo "the mocks are not running: start them with make up" >&2
  exit 1
fi

(cd "$ROOT/wallet" && go build -o "$BIN/api" ./cmd/api && go build -o "$BIN/worker" ./cmd/worker)
(cd "$ROOT/simulator" && go build -o "$BIN/simulator" .)

"$BIN/api" 2>&1 | sed 's/^/[api] /' & pids+=($!)
"$BIN/worker" 2>&1 | sed 's/^/[worker] /' & pids+=($!)
(cd "$ROOT/simulator" && "$BIN/simulator" 2>&1 | sed 's/^/[simulator] /') & pids+=($!)

echo "simulator: http://localhost:8090   grafana: http://localhost:3000"
wait
