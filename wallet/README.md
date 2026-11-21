# Wallet

The digital wallet and its Pix integration layer: accounts, balances, a ledger, and the participant side of the DICT and
the SPI. It is the product of this project; the mocks next to it (`../mock`) only stand in for Bacen.

> **Status: the core, the keys, paying a key of another institution and paying another customer of the wallet and receiving payments are done.** Stack decided (Go, gRPC, pgx, golang-migrate, modular monolith; see the
> [roadmap](../docs/roadmap.md#decision-the-stack-of-the-wallet)). The ledger (accounts, balances, reservation) works and
> is tested on PostgreSQL. The DICT client and the SPI client (`internal/pix`) are in place, with returns and reports. A customer can pay a key of another
> institution or another customer of the wallet, and receive payments, end to end; returning and the statement are next.

## Independence from the mocks

Inside the same repository, the wallet is a separate system:

- **Its own code and dependencies.** It never imports, copies or reads anything from `mock/`. It talks to the mocks over
  HTTP and XML, as it will talk to Bacen.
- **Its own database.** A PostgreSQL of its own (`:5434`, volume `wallet-pgdata`), with its own migrations and seed. It
  never connects to the mocks' databases and the mocks never see the wallet's.
- **Its own infrastructure and commands.** `wallet/docker-compose.yml` (project `pixwallet`) and `wallet/Makefile`. The
  root `make setup` starts only the mocks; `make up` starts both sides.
- **Its own tests.** Unit, repository and end-to-end tests live under `wallet/`. The root `e2e/` is the closing test of
  the mocks and stays that way.
- **Configuration only.** The mocks' URLs are in `.env` (`DICT_BASE_URL`, `SPI_BASE_URL`). Switching to the real
  endpoints is changing them.

## Layout

```
wallet/
├── api/proto/wallet/v1/   # the gRPC contract: account.proto, key.proto, payment.proto
├── migrations/            # SQL migrations (golang-migrate), embedded in the binary
├── gen/                   # generated Go code (make proto), committed for the clients
├── cmd/api/               # process 1: the gRPC API
├── cmd/worker/            # process 2: the SPI stream consumer, started next to the API
├── cmd/seed/              # applies the migrations and loads the test accounts and their keys (idempotent)
└── internal/
    ├── ledger/            # module: accounts, simple balance with reservation
    ├── payment/           # module: payment flows and status
    ├── pix/               # module: DICT and SPI clients (the integration layer)
    ├── pixkey/            # module: the wallet's Pix keys (registers them in the DICT)
    ├── grpcapi/           # adapter: gRPC services
    ├── seed/              # test accounts and their Pix keys, same people and keys as the wallet's accounts in the DICT seed
    └── platform/
        ├── config/        # environment configuration
        └── db/            # pgx pool, transactions (InTx) and migrations; dbtest/ for integration tests
```

Modules use each other only through their exported API, never each other's tables, so the monolith can be split into
services later.

## Running the base

```sh
make up          # from the repository root: both mocks and the wallet (its database, migrations and test accounts)
```

Generate the gRPC code after changing a `.proto`, from `wallet/`:

```sh
make proto       # needs protoc, protoc-gen-go and protoc-gen-go-grpc
```

Run the two processes (each in its own terminal, after `make up`):

```sh
go run ./cmd/api       # gRPC on :9090, with the standard health service
go run ./cmd/worker    # reads the SPI stream and settles the payments; without it they stay PENDING
```

Or only the wallet's side, from `wallet/`: `make up` (PostgreSQL on `:5434`, migrations applied, test accounts and their keys loaded). To stop it or to
start with an empty database: `docker compose down` or `docker compose down -v` inside `wallet/`.

The mocks alone are `make setup` at the root (DICT on `:8080`, SPI on `:8081`). How to call them is in the
[integration guide](../docs/integration-guide.md).

## gRPC contract

Three services in `api/proto/wallet/v1`, one per module. Implemented so far: `GetBalance` and the whole `KeyService`; the rest is
the contract the features will fill.

| Service | Calls |
|---------|-------|
| `AccountService` | `GetBalance`, `GetStatement` |
| `KeyService` | `RegisterKey`, `ListKeys`, `RemoveKey` |
| `PaymentService` | `ResolveKey`, `CreatePayment`, `GetPayment`, `ListPayments`, `ReturnPayment` |

To pay, call `ResolveKey` (it shows who owns the key) and then `CreatePayment` with the `resolution_id`. A payment through the SPI
comes back `PENDING` and is read later with `GetPayment` or `ListPayments`; a payment to another customer of the wallet comes back
`SETTLED` in the same call. Money is in centavos. The decisions are in the [roadmap](../docs/roadmap.md#decision-the-stack-of-the-wallet).

## Paying a key of another institution

Module `internal/payment`. `ResolveKey` creates the `EndToEndId`, looks the key up in the DICT and keeps the answer for 5 minutes (a
key of the wallet itself answers `Unimplemented` for now). `CreatePayment` reserves the amount in the ledger (the reference is the
`EndToEndId`) and sends the `pacs.008`; the payment is `PENDING` until the SPI answers.

- **One resolution, at most one payment.** Repeating `CreatePayment` returns the payment that exists, or finishes the one that stopped
  halfway (reserved but not sent); with another amount or message it is `ALREADY_EXISTS`. Concurrent calls make one payment.
- **The SPI being down does not fail the payment.** It stays `PENDING`, the money stays reserved, and the worker sends it again
  (every 5 s, for what is older than 10 s). The SPI is idempotent by `EndToEndId`, so sending twice is safe. A `400` from the SPI means
  it never took the order, so the payment is rejected and the money goes back.
- **The worker settles.** `cmd/worker` reads the SPI stream: `ACSC` settles (the reserved money leaves the account), `RJCT` gives it
  back with the reason of `ReasonOf` (see the SPI client), and an `admi.002` that names the order rejects it. A batch is acknowledged only
  after all of it was applied; one that fails is delivered again, and applying a status twice changes nothing.
- **The timeout is the SPI's.** A payment nobody answers in 40 s comes back `RJCT` with `AB03`. The wallet never rejects on its own a
  payment the SPI may have taken, or the money could leave twice.
- **Not handled yet by the worker:** returns (`pacs.004`) are logged and skipped.

## Receiving a payment

The worker answers each `pacs.008` the SPI sends to the wallet. The account is found by branch and number: no such account is `AC03`,
a tax id that is not the holder's is `BE01`, an account type that does not match is `AC14`. A refusal is only answered (`pacs.002`
`RJCT`); nothing is recorded, since no money moved. An accepted order is recorded as an `INBOUND` payment, `PENDING`, and answered
`ACSP`. The money is credited only when the SPI settles it (`ACCC`), by reference to the `EndToEndId`, and the payment becomes `SETTLED`;
an `RJCT` from the SPI (such as `AB03`) rejects it with no credit.

- **Delivered again.** The same order twice is answered again and recorded once; an order already settled or rejected is not answered.
- **Answer that cannot be sent.** The batch is not acknowledged, so the SPI delivers it again and the answer is retried.
- **The answer must reach the SPI within 40 s,** or the SPI rejects the payment with `AB03` at the payer.
- **Not checked:** account status (the wallet has no closed or blocked accounts, so `AC06` and `AC07` never come from it), amount
  limits (`AM02`) and salary accounts (`AG03`, which `AC14` covers since the wallet only has `CACC`, `SVGS` and `TRAN`).

## Paying another customer of the wallet (book transfer)

Same two calls. `ResolveKey` looks at the wallet's own records first (`pixkey.AccountOf`): a key of another customer answers
`book_transfer = true` with the payee's data, **without asking the DICT** (which would refuse it); a key of the paying account is
`INVALID_ARGUMENT`. The wallet's own copy of the keys must agree with the DICT; the seed loads both. A key the DICT calls ours but the
wallet does not know is an error (`ErrKeyNotRecorded`), never a payment through the SPI, which would refuse it with `AG12`.

- **One step.** `CreatePayment` records the payment, then `ledger.Transfer` debits the payer and credits the payee in a single
  database transaction (idempotent by `EndToEndId`: a repeat moves nothing), records the payment the payee receives (`INBOUND`, same
  `EndToEndId`, so each side sees it in `ListPayments`) and closes the first as `SETTLED`. No reservation, nothing sent to the SPI.
- **Without enough money** it is `FAILED_PRECONDITION` and the attempt leaves nothing behind, so the same resolution can be paid after
  a deposit.
- **A call that stops halfway can be repeated,** and the worker finishes what it finds pending after 10 s. The transfer is atomic, so
  the money is never half moved.
- **Reported afterwards.** The worker sends a `trck.002` for each settled book transfer (scenario `BOK1`, initiation `DICT` when the key
  was used), up to 10 per call. The `camt.025` receipt marks it accepted; one that is not answered in a minute is sent again (the SPI is
  idempotent per transaction); a refusal worth trying again (`ED05`) is sent again, and any other is final and logged as an error for
  operations, since the report was built by the wallet.
- **The 40 seconds** of the Pix rules are met by settling in the same call, not enforced by a clock: nothing rejects a transfer that
  took longer.

## DICT client

`internal/pix` has the client of the DICT API v2 (`NewDictClient(baseURL, ispb)`): `GetEntry`, `CreateEntry`, `UpdateEntry`,
`DeleteEntry` and `CheckKeys`, plus `NewEndToEndID` and `NewRequestID`. Failures come back as `*APIError` that matches
`ErrNotFound`, `ErrConflict`, `ErrBookTransfer`, `ErrRateLimited`, `ErrUnavailable` and others with `errors.Is`; `pix.Retryable`
tells whether trying again can work. The client does not retry: that depends on the payment flow.

## SPI client

`NewSPIClient(baseURL, ispb)` in `internal/pix`: `Send` (1 to 10 messages), `Pull` (reads the stream; pass `""` to open it, then
the returned next path) and `Ack` (closes the stream at a next path). The messages of the payment cycle: `NewPacs008` and
`ParsePacs008` (the payment order), `NewPacs002` and `ParsePacs002` (the status), `ParseAdmi002` (the SPI could not process a
message) and `MessageKind` to tell what came from the stream.

Returns and reports:

- **Return.** `NewReturnID` creates the `RtrId` (create it before sending and keep it); `NewPacs004` builds the return of a
  payment the wallet received, and `ParsePacs004` reads the return of a payment the wallet made. The SPI's `pacs.002` answers
  a return with the `RtrId` as `InstrID`, and the wallet answers a return it receives with `NewPacs002` the same way. A return
  to the same participant is refused by the builder: it is a ledger movement, not a message.
- **Report of transactions settled outside the SPI.** `NewTrck002` builds the report of 1 to 500 `ReportedTx` (a book transfer,
  or a return inside the institution) and returns its `MsgId`; `ParseCamt025` reads the SPI's receipts, one per transaction
  (`ACPT`, or `RJCT` with a reason), which name that `MsgId`. The builder applies the rules of the contract before sending
  (positive amount, not a salary account, settled at most 30 days ago and not in the future, the Pix key present or absent
  according to how the payment was initiated).

Rejections. `ReasonOf(code)`, or `Rejection()` on a `TxStatus`, a `Receipt` or a `Notification`, turns a reason code of the SPI into a
`Rejection`: the `Cause` (`recipient`: the receiving side cannot take it; `rule`: the Pix rules forbid it; `transient`: a timeout or
processing error; `wallet`: the wallet's own fault or limit; `unknown`), whether the same payment can work if made again later
(`TryAgain`, as a new payment with a new `EndToEndId`), a `Message` that is safe to show the customer, and `Alert()` for what
operations must look at. A rejection is final, so the reserved balance goes back whatever the cause. The message never repeats the text
another institution sent and never says a payment was refused on suspicion of fraud. A code nobody mapped is `unknown`, never an error.

Like the DICT client, it does not retry; a message sent again is safe, since the SPI is idempotent by `EndToEndId`, `RtrId`
and `MsgId`.

## Test accounts

`make up` loads three accounts, with the same people, branch and account numbers as the wallet's accounts in the DICT
seed. Their IDs are fixed, so they can be used by hand:

| ID | Holder | Account | Balance |
|----|--------|---------|---------|
| `0a1b2c3d-0001-4000-8000-000000000001` | Ana Souza | `0001` / `00123456` | R$ 1.000,00 |
| `0a1b2c3d-0002-4000-8000-000000000002` | Bruno Lima | `0001` / `00234567` | R$ 500,00 |
| `0a1b2c3d-0003-4000-8000-000000000003` | Padaria Pão Quente Ltda | `0001` / `00345678` | R$ 0,00 |

The seed does not change accounts that already exist. A database created before account types existed keeps every account as
`CACC`; to get Bruno's savings account, start from an empty one (`docker compose down -v` in `wallet/`, then `make up`).

With the API running, `GetBalance` of Ana (for example with `grpcurl`, or a client generated from `gen/`) returns
`available_cents: 100000`.

## Tests

```sh
go test ./...           # unit tests; the ones that need PostgreSQL are skipped
make test-integration   # all of them, on the wallet's PostgreSQL (needs Docker); each test gets its own schema
make e2e                # from the repository root: the wallet's API and the DICT mock together, over gRPC (`wallet/e2e`)
WALLET_TEST_DICT_URL=http://localhost:8080/api/v2 WALLET_TEST_SPI_URL=http://localhost:8081/api/v1 go test ./internal/pix   # also the DICT and SPI clients against the running mocks (`make setup`)
```

## Ports

| Port | Who |
|------|-----|
| 8080 | DICT mock |
| 8081 | SPI mock |
| 5432, 5433 | PostgreSQL of the DICT and SPI mocks |
| 5434 | PostgreSQL of the wallet |
| 9090 | gRPC API of the wallet |
