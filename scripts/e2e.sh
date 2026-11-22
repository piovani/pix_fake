#!/usr/bin/env bash
# Runs the end-to-end tests: starts PostgreSQL for the DICT mock and the SPI mock, loads the seed data, starts both
# mocks and runs e2e/closing_test.go against them (the mocks together). Then starts the wallet's API and its worker on a
# throwaway schema of the wallet's PostgreSQL, pointed at those mocks, and runs wallet/e2e against them (the wallet's
# flows). Needs Docker and Go.
#
#   make e2e
#
# The mocks listen on 18080 (DICT) and 18081 (SPI) and the wallet on 19090, so what you already have running on the
# usual ports is not touched. The wallet leg uses its own schema in the wallet's database, dropped at the end, so the data
# of `make up` stays. The SPI runs with a 2-second payment timeout, to try the timeout scenario without a 40-second wait.
# The PostgreSQL containers are left running; stop them with `make db-down` in mock/dict and mock/spi, and
# `docker compose down` in wallet/.
#
# With WALLET_COVERDIR set, the wallet's binaries are built with -cover and write their coverage there (see coverage.sh).
# With E2E_LOG_DIR set, the logs of the mocks, the API and the worker are copied there at the end.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DICT_PORT="${DICT_PORT:-18080}"
SPI_PORT="${SPI_PORT:-18081}"
WALLET_PORT="${WALLET_PORT:-19090}"
TIMEOUT="${PIX_E2E_TIMEOUT:-2s}"
SCHEMA=""

WORK="$(mktemp -d)"
pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  # a covered binary writes its counters when it exits, so wait for it
  for pid in "${pids[@]:-}"; do wait "$pid" 2>/dev/null || true; done
  if [ -n "$SCHEMA" ]; then
    (cd "$ROOT/wallet" && docker compose exec -T postgres psql -q -U wallet -d wallet -c "DROP SCHEMA IF EXISTS $SCHEMA CASCADE") >/dev/null 2>&1 || true
  fi
  if [ -n "${E2E_LOG_DIR:-}" ]; then mkdir -p "$E2E_LOG_DIR" && cp "$WORK"/*.log "$E2E_LOG_DIR"/ 2>/dev/null || true; fi
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
(cd "$ROOT/wallet" && docker compose up -d --wait)

echo "==> building"
(cd "$ROOT/mock/dict" && go build -o "$WORK/dict" ./cmd/dict && go build -o "$WORK/dict-seed" ./cmd/seed)
(cd "$ROOT/mock/spi" && go build -o "$WORK/spi" ./cmd/spi && go build -o "$WORK/spi-seed" ./cmd/seed)
COVER=()
if [ -n "${WALLET_COVERDIR:-}" ]; then
  mkdir -p "$WALLET_COVERDIR"
  COVER=(-cover "-coverpkg=./internal/...,./cmd/...")
fi
(cd "$ROOT/wallet" && for c in api worker seed accounts reports; do go build ${COVER[@]+"${COVER[@]}"} -o "$WORK/wallet-$c" "./cmd/$c" || exit 1; done)

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

if [ "$rc" -eq 0 ]; then
  echo "==> starting the wallet on a throwaway schema"
  SCHEMA="e2e_$(date +%s)_$$"
  (cd "$ROOT/wallet" && docker compose exec -T postgres psql -q -U wallet -d wallet -c "CREATE SCHEMA $SCHEMA")
  export WALLET_DATABASE_URL="postgres://wallet:wallet@localhost:5434/wallet?sslmode=disable&search_path=$SCHEMA"
  if [ -n "${WALLET_COVERDIR:-}" ]; then export GOCOVERDIR="$WALLET_COVERDIR"; fi
  "$WORK/wallet-seed"
  WALLET_GRPC_ADDR=":$WALLET_PORT" DICT_BASE_URL="http://localhost:$DICT_PORT/api/v2" SPI_BASE_URL="http://localhost:$SPI_PORT/api/v1" \
    "$WORK/wallet-api" >"$WORK/wallet.log" 2>&1 &
  pids+=("$!")
  for _ in $(seq 1 50); do
    if (exec 3<>"/dev/tcp/localhost/$WALLET_PORT") 2>/dev/null; then break; fi
    sleep 0.2
  done
  # the API has applied the migrations by now; the worker settles the payments
  DICT_BASE_URL="http://localhost:$DICT_PORT/api/v2" SPI_BASE_URL="http://localhost:$SPI_PORT/api/v1" \
    "$WORK/wallet-worker" >"$WORK/wallet-worker.log" 2>&1 &
  pids+=("$!")

  echo "==> running the wallet end-to-end tests"
  (cd "$ROOT/wallet" && WALLET_E2E_GRPC_ADDR="localhost:$WALLET_PORT" WALLET_E2E_DICT_URL="http://localhost:$DICT_PORT/api/v2" \
    WALLET_E2E_SPI_URL="http://localhost:$SPI_PORT/api/v1" WALLET_E2E_BIN="$WORK" WALLET_E2E_DATABASE_URL="$WALLET_DATABASE_URL" \
    go test -count=1 -v ./e2e) || rc=$?
fi

if [ "$rc" -ne 0 ]; then
  echo "==> a test failed; the logs:" >&2
  tail -n 30 "$WORK/dict.log" "$WORK/spi.log" "$WORK/wallet.log" "$WORK/wallet-worker.log" >&2 2>/dev/null || true
fi
exit "$rc"
