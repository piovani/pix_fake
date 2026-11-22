#!/usr/bin/env bash
# Measures the coverage of the wallet: the end-to-end run (make e2e, with the API, the worker and the operations commands
# built with -cover) and the unit and PostgreSQL tests, each on its own and merged. Needs what make e2e needs, with
# make setup stopped.
#
#   make coverage
#
# The raw data and an HTML report are left in .coverage/ at the repository root. With COVERAGE_MIN set (a percentage),
# it fails when the merged coverage of internal/ is below it. In GitHub Actions the table also goes to the run summary.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/.coverage"
rm -rf "$OUT"
mkdir -p "$OUT/e2e" "$OUT/unit" "$OUT/all"

echo "==> end to end, with covered binaries"
WALLET_COVERDIR="$OUT/e2e" "$ROOT/scripts/e2e.sh" >"$OUT/e2e.log" 2>&1 || { tail -n 40 "$OUT/e2e.log"; exit 1; }

echo "==> unit and PostgreSQL tests"
(cd "$ROOT/wallet" && WALLET_TEST_DATABASE_URL="${WALLET_TEST_DATABASE_URL:-postgres://wallet:wallet@localhost:5434/wallet?sslmode=disable}" \
  go test -count=1 -cover -coverpkg=./internal/...,./cmd/... ./internal/... ./cmd/... -args -test.gocoverdir="$OUT/unit" >"$OUT/unit.log" 2>&1) \
  || { tail -n 40 "$OUT/unit.log"; exit 1; }

go tool covdata merge -i="$OUT/e2e,$OUT/unit" -o "$OUT/all"
for set in e2e unit all; do
  go tool covdata textfmt -i="$OUT/$set" -o "$OUT/$set.out"
done

cd "$ROOT/wallet"
# dbtest is a helper of the tests, not code of the wallet
total() { (head -1 "$1"; grep -v '^mode' "$1" | grep -v '/dbtest/' | grep "${2:-}") >"$1.part"; go tool cover -func="$1.part" | awk '/^total:/ {print $3}'; }
echo
{
printf '%-36s %8s %8s %8s\n' package e2e unit all
for pkg in $(go tool covdata percent -i="$OUT/all" | awk '{print $1}' | grep -v /dbtest | sort); do
  pct() { go tool covdata percent -i="$1" -pkg="$pkg" 2>/dev/null | awk '{print $3}'; }
  printf '%-36s %8s %8s %8s\n' "${pkg#pixfake/wallet/}" "$(pct "$OUT/e2e")" "$(pct "$OUT/unit")" "$(pct "$OUT/all")"
done
printf '%-36s %8s %8s %8s\n' "total internal/" "$(total "$OUT/e2e.out" /internal/)" "$(total "$OUT/unit.out" /internal/)" "$(total "$OUT/all.out" /internal/)"
printf '%-36s %8s %8s %8s\n' "total (internal/ and cmd/)" "$(total "$OUT/e2e.out")" "$(total "$OUT/unit.out")" "$(total "$OUT/all.out")"
} | tee "$OUT/summary.txt"
go tool cover -html="$OUT/all.out" -o "$OUT/coverage.html"
echo
echo "report: $OUT/coverage.html"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  { echo "### Wallet coverage"; echo; echo '```'; cat "$OUT/summary.txt"; echo '```'; } >>"$GITHUB_STEP_SUMMARY"
fi

if [ -n "${COVERAGE_MIN:-}" ]; then
  got="$(total "$OUT/all.out" /internal/)"
  if awk -v g="${got%\%}" -v m="$COVERAGE_MIN" 'BEGIN { exit !(g < m) }'; then
    echo "coverage of internal/ is $got, below the minimum of $COVERAGE_MIN%" >&2
    exit 1
  fi
  echo "coverage of internal/ is $got (minimum $COVERAGE_MIN%)"
fi
