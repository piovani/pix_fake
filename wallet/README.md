# Wallet

A digital wallet that is a Pix participant: accounts, a ledger, Pix keys, payments in and out, and the MED. The mocks in
`../mock` stand in for Bacen; the wallet talks to them over HTTP/XML only and never imports their code, so pointing it
at the real endpoints is a config change (`DICT_BASE_URL`, `SPI_BASE_URL`, `WALLET_ISPB`).

Go, gRPC, pgx, golang-migrate, OpenTelemetry. One module, two processes: the API (`cmd/api`) and the worker
(`cmd/worker`), which reads the SPI stream and runs the periodic jobs. Its own PostgreSQL on `:5434`.

## Layout

```
api/proto/wallet/v1/   gRPC contract
gen/                   generated code (make proto), committed
migrations/            SQL, embedded in the binary
cmd/api, cmd/worker    the two processes
cmd/seed               migrations + test accounts and keys
cmd/accounts           ops: block/close an account, set its receive limit
cmd/reports            ops: list and resend refused book-transfer reports
cmd/keys               ops: reconcile the keys with the DICT and list the ones that disagree
cmd/med                ops: analyse the MED reports against the wallet's customers
internal/ledger        accounts, balance with reservations and blocks, movement log
internal/pixkey        the wallet's keys, registered in the DICT
internal/payment       payment flows and their status
internal/med           the MED, as the payer's and as the payee's participant
internal/pix           DICT and SPI clients, ISO 20022 messages
internal/grpcapi       gRPC adapter, no business rules
internal/platform      config, db, telemetry
```

Modules only use each other's exported API, never each other's tables.

## Running

```sh
make up                  # repo root: both mocks + the wallet's database, migrated and seeded
go run ./cmd/api         # gRPC on :9090
go run ./cmd/worker      # without it, payments stay PENDING
make proto               # after changing a .proto
```

`docker compose down -v` in `wallet/` starts the database from scratch. Telemetry goes to
`OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`, the Grafana of `make up`); `OTEL_SDK_DISABLED=true`
turns it off.

## API

| Service | Calls |
|---------|-------|
| `AccountService` | `GetBalance`, `GetStatement` |
| `KeyService` | `RegisterKey`, `ListKeys`, `RemoveKey` |
| `PaymentService` | `ResolveKey`, `CreatePayment`, `GetPayment`, `ListPayments`, `ReturnPayment` |
| `ContestService` | `ContestPayment`, `GetContest`, `ListContests`, `CancelContest` |

Paying is two calls: `ResolveKey` shows who owns the key, `CreatePayment` pays the `resolution_id`. Money is in
centavos. Idempotency keys come from the client (`resolution_id`, `request_id`).

## Flows

- **Paying another bank.** `CreatePayment` reserves the amount and sends a `pacs.008`; the payment is `PENDING` until
  the worker reads `ACSC` (settled) or `RJCT` (released). One resolution makes at most one payment, so a call can be
  repeated after any failure. If the SPI is down the worker resends it. The 40 s timeout is the SPI's (`AB03`): the
  wallet never rejects a payment the SPI might have taken.
- **Book transfer** (both customers are the wallet's). The key is found in the wallet's records, never in the DICT; the
  money moves in one transaction and the call returns `SETTLED`. The worker reports it to the SPI afterwards
  (`trck.002`); a refused report shows up in `cmd/reports list` and can be resent.
- **Receiving.** The worker answers each `pacs.008`: `AC03` unknown account, `BE01` wrong tax id, `AC14` wrong account
  type, `AC06`/`AC07` blocked/closed, `AM02` above the receive limit. It credits only on `ACCC`.
- **Returning.** `ReturnPayment` sends back all or part of a received payment (up to 90 days, up to what is left). A
  `pacs.004` from another bank credits the payer on `ACCC`.
- **Statement.** `GetStatement` is the money that moved; `ListPayments` is every payment with its status.
- **Keys.** `cmd/keys check` reconciles the wallet's keys with the DICT (CIDs, VSync per key type, the DICT's CID file
  where they differ) and lists `MISSING_IN_DICT`, `DATA_DIFFERS` and `ONLY_IN_DICT`; it exits with 1 if anything
  disagrees. The worker runs it every 6 hours into the gauge `wallet.keys.out_of_sync` (the DICT allows 40 CID files a
  day); the log carries only counts, since keys are personal data.

**Worker jobs**, every 5 s: resend what the SPI didn't take, finish interrupted book transfers, send reports, release
reservations no payment points to (after 10 min), and follow the MED.

## MED

The DICT calls no one: the worker reads its lists every 5 s, from watermarks it keeps.

**As the payer's participant** (a customer contests a Pix they sent):

- `ContestPayment` takes a Pix sent to another bank and settled up to 80 days ago, with the situation (scam, account
  takeover, coercion, fraudulent access, other) and details. One contest per payment can be in progress.
- The wallet reports it in the DICT at once; if the DICT can't be reached the contest stays `OPENING` and the worker
  reports it. `CancelContest` withdraws it while the other bank has not analysed it.
- The other bank agrees: the wallet asks for the refund and the contest is `REFUND_REQUESTED`. It disagrees: `REJECTED`.
- The money comes back as a `pacs.004` `FR01`, credited like any return. When the refund is closed the contest is
  `REFUNDED` (with `refunded_cents` and the return's `RtrId`), or `REJECTED` with a reason for the user.

**As the payee's participant** (another bank reports a payment the wallet received):

- The worker acknowledges the report and blocks the money: what the report asks, or what is available when less. A
  block holds even on a blocked account and shows as reserved in `GetBalance`.
- An operator analyses it:

  ```sh
  go run ./cmd/med list                                         # id, transaction, situation, asked, blocked, details
  go run ./cmd/med agree <report-id> SCAMMER_ACCOUNT "detalhes"   # or APPLICATION_FRAUD, MULE_ACCOUNT, OTHER
  go run ./cmd/med disagree <report-id> "pagamento legítimo"      # gives the block back
  ```

- A report the other bank cancels gives the block back too.
- A refund asked of the wallet is answered with a `pacs.004` (`FR01`, or `BE08` for an operational flaw) from the block
  and, if it asks for more, from what else is available. Once the return settles the refund is closed with its `RtrId`:
  `TOTALLY_ACCEPTED`, `PARTIALLY_ACCEPTED`, or `REJECTED` (`NO_BALANCE`) when there is nothing to return.

Not built yet: funds recovery from the wallet.

## Metrics

`wallet.payments.finished` (by direction, type, status, reason), `wallet.payments.amount`, `wallet.keys.out_of_sync`,
`wallet.med.blocked`, `wallet.med.refunds`, `wallet.med.contests`.

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
