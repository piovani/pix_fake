#!/usr/bin/env bash
# Runs the closing test: starts PostgreSQL for the DICT mock and the SPI mock, loads the seed data, starts both
# mocks, runs e2e/closing_test.go against them and stops the mocks. Needs Docker and Go.
#
#   make e2e
#
# The mocks listen on 18080 (DICT) and 18081 (SPI), so a mock you already have running on its usual port is
# not touched. The SPI runs with a 2-second payment timeout, to try the timeout scenario without a 40-second wait.
# The PostgreSQL containers are left running; stop them with `make db-down` in mock/dict and mock/spi.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DICT_PORT="${DICT_PORT:-18080}"
SPI_PORT="${SPI_PORT:-18081}"
TIMEOUT="${PIX_E2E_TIMEOUT:-2s}"

WORK="$(mktemp -d)"
pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  rm -rf "$WORK"
}
trap cleanup EXIT

wait_healthy() { # url name
  for _ in $(seq 1 100); do
    if curl -fs "$1/healthz" >/dev/null 2>&1; then return 0; fi
    sleep 0.2
  done
  echo "$2 did not become healthy; its log:" >&2
  tail -n 30 "$WORK/$2.log" >&2 || true
  return 1
}

echo "==> starting PostgreSQL"
(cd "$ROOT/mock/dict" && docker compose up -d --wait postgres)
(cd "$ROOT/mock/spi" && docker compose up -d --wait postgres)

echo "==> building"
(cd "$ROOT/mock/dict" && go build -o "$WORK/dict" ./cmd/dict && go build -o "$WORK/dict-seed" ./cmd/seed)
(cd "$ROOT/mock/spi" && go build -o "$WORK/spi" ./cmd/spi && go build -o "$WORK/spi-seed" ./cmd/seed)

echo "==> loading the seed data"
"$WORK/dict-seed"
"$WORK/spi-seed"

echo "==> starting the mocks"
DICT_ADDR=":$DICT_PORT" "$WORK/dict" >"$WORK/dict.log" 2>&1 &
pids+=("$!")
SPI_ADDR=":$SPI_PORT" SPI_POLL_WAIT=1s SPI_TIMEOUT="$TIMEOUT" "$WORK/spi" >"$WORK/spi.log" 2>&1 &
pids+=("$!")
wait_healthy "http://localhost:$DICT_PORT" dict
wait_healthy "http://localhost:$SPI_PORT" spi

echo "==> running the closing test"
rc=0
(cd "$ROOT/e2e" && DICT_URL="http://localhost:$DICT_PORT" SPI_URL="http://localhost:$SPI_PORT" PIX_E2E_TIMEOUT="$TIMEOUT" \
  go test -count=1 -v ./...) || rc=$?

if [ "$rc" -ne 0 ]; then
  echo "==> the test failed; the mocks' logs:" >&2
  tail -n 30 "$WORK/dict.log" "$WORK/spi.log" >&2 || true
fi
exit "$rc"
