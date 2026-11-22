# Wallet

A digital wallet that is a Pix participant: accounts, a ledger, Pix keys, and payments in and out through the DICT and
the SPI. The mocks in `../mock` stand in for Bacen; the wallet talks to them over HTTP/XML only and never imports their
code, so pointing it at the real endpoints is a config change (`DICT_BASE_URL`, `SPI_BASE_URL`, `WALLET_ISPB`).

Go, gRPC, pgx, golang-migrate. One module, two processes: the API (`cmd/api`) and the worker (`cmd/worker`), which reads
the SPI stream and runs the periodic jobs. Its own PostgreSQL on `:5434`.

## Layout

```
api/proto/wallet/v1/   gRPC contract
gen/                   generated code (make proto), committed
migrations/            SQL, embedded in the binary
cmd/api, cmd/worker    the two processes
cmd/seed               migrations + test accounts and keys
cmd/accounts           ops: block/close an account, set its receive limit
cmd/reports            ops: list and resend refused book-transfer reports
internal/ledger        accounts, balance with reservations, movement log
internal/pixkey        the wallet's keys, registered in the DICT
internal/payment       payment flows and their status
internal/pix           DICT and SPI clients, ISO 20022 messages
internal/grpcapi       gRPC adapter, no business rules
```

Modules only use each other's exported API, never each other's tables.

## Running

```sh
make up                  # repo root: both mocks + the wallet's database, migrated and seeded
go run ./cmd/api         # gRPC on :9090
go run ./cmd/worker      # without it, payments stay PENDING
make proto               # after changing a .proto
```

`docker compose down -v` in `wallet/` starts the database from scratch.

## API

| Service | Calls |
|---------|-------|
| `AccountService` | `GetBalance`, `GetStatement` |
| `KeyService` | `RegisterKey`, `ListKeys`, `RemoveKey` |
| `PaymentService` | `ResolveKey`, `CreatePayment`, `GetPayment`, `ListPayments`, `ReturnPayment` |

Paying is two calls: `ResolveKey` shows who owns the key, `CreatePayment` pays the `resolution_id`. Money is in centavos.
Idempotency keys come from the client (`resolution_id`, `request_id`).

## How the flows behave

**Paying another bank.** `CreatePayment` reserves the amount and sends a `pacs.008`; the payment is `PENDING` until the
worker reads `ACSC` (settled) or `RJCT` (released). One resolution makes at most one payment, so a call can be repeated
after any failure. If the SPI is down the payment stays pending and the worker resends it. The 40 s timeout is the SPI's
(`AB03`): the wallet never rejects a payment the SPI might have taken.

**Paying another wallet customer (book transfer).** The key is found in the wallet's own records, never in the DICT. The
money moves in one DB transaction and the call returns `SETTLED`. The worker reports it to the SPI afterwards (`trck.002`,
`BOK1`); a refused report shows up in `cmd/reports list` and can be resent once fixed.

**Receiving.** The worker answers each `pacs.008`: `AC03` unknown account, `BE01` wrong tax id, `AC14` wrong account type,
`AC06`/`AC07` blocked/closed, `AM02` above the account's limit. An accepted one is credited only on `ACCC`.

**Returning.** `ReturnPayment` sends back all or part of a received payment (at most 90 days old, up to what is left). A
`pacs.004` from another bank, returning something the wallet paid, credits the payer on `ACCC`.

**Statement.** `GetStatement` is the money that moved (ledger log); `ListPayments` is every payment with its status,
in flight and rejected included.

**Worker jobs**, every 5 s: resend what the SPI didn't take, finish interrupted book transfers, send reports, release
reservations no payment points to (after 10 min, longer than a resolution lives).

## Test accounts

| ID | Holder | Branch / account | Balance |
|----|--------|------------------|---------|
| `0a1b2c3d-0001-4000-8000-000000000001` | Ana Souza | `0001` / `00123456` | R$ 1.000,00 |
| `0a1b2c3d-0002-4000-8000-000000000002` | Bruno Lima | `0001` / `00234567` (savings) | R$ 500,00 |
| `0a1b2c3d-0003-4000-8000-000000000003` | Padaria Pão Quente Ltda | `0001` / `00345678` | R$ 0,00 |

Same people and keys as the wallet's entries in the DICT seed.

## Tests

```sh
go test ./...            # unit; PostgreSQL tests are skipped without WALLET_TEST_DATABASE_URL
make test-integration    # everything on PostgreSQL
make e2e                 # repo root: API + worker + mocks, over gRPC (stop make up first)
make coverage            # repo root: e2e and unit coverage, merged
```

`WALLET_TEST_DICT_URL` and `WALLET_TEST_SPI_URL` also run the clients against the mocks of `make setup`.

## Ports

8080 DICT mock · 8081 SPI mock · 5432/5433 mocks' PostgreSQL · 5434 wallet's PostgreSQL · 9090 wallet API
