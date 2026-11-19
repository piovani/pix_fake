# Wallet

The digital wallet and its Pix integration layer: accounts, balances, a ledger, and the participant side of the DICT and
the SPI. It is the product of this project; the mocks next to it (`../mock`) only stand in for Bacen.

> **Status: the core is done, and `RegisterKey` works.** Stack decided (Go, gRPC, pgx, golang-migrate, modular monolith; see the
> [roadmap](../docs/roadmap.md#decision-the-stack-of-the-wallet)). The ledger (accounts, balances, reservation) works and
> is tested on PostgreSQL. The DICT client and the first part of the SPI client (`internal/pix`) are in place; returns, reports and the payment flows are next.

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
├── cmd/seed/              # applies the migrations and loads the test accounts (idempotent)
└── internal/
    ├── ledger/            # module: accounts, simple balance with reservation
    ├── payment/           # module: payment flows and status
    ├── pix/               # module: DICT and SPI clients (the integration layer)
    ├── pixkey/            # module: the wallet's Pix keys (registers them in the DICT)
    ├── grpcapi/           # adapter: gRPC services
    ├── seed/              # test accounts, same people as the wallet's accounts in the DICT seed
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
go run ./cmd/worker
```

Or only the wallet's side, from `wallet/`: `make up` (PostgreSQL on `:5434`, migrations applied, test accounts loaded). To stop it or to
start with an empty database: `docker compose down` or `docker compose down -v` inside `wallet/`.

The mocks alone are `make setup` at the root (DICT on `:8080`, SPI on `:8081`). How to call them is in the
[integration guide](../docs/integration-guide.md).

## gRPC contract

Three services in `api/proto/wallet/v1`, one per module. Only `GetBalance` and `RegisterKey` are implemented so far; the rest is
the contract the features will fill.

| Service | Calls |
|---------|-------|
| `AccountService` | `GetBalance`, `GetStatement` |
| `KeyService` | `RegisterKey`, `ListKeys`, `RemoveKey` |
| `PaymentService` | `ResolveKey`, `CreatePayment`, `GetPayment`, `ListPayments`, `ReturnPayment` |

To pay, call `ResolveKey` (it shows who owns the key) and then `CreatePayment` with the `resolution_id`. A payment through the SPI
comes back `PENDING` and is read later; a payment to another customer of the wallet comes back `SETTLED`. Money is in centavos.
The decisions are in the [roadmap](../docs/roadmap.md#decision-the-stack-of-the-wallet).

## DICT client

`internal/pix` has the client of the DICT API v2 (`NewDictClient(baseURL, ispb)`): `GetEntry`, `CreateEntry`, `UpdateEntry`,
`DeleteEntry` and `CheckKeys`, plus `NewEndToEndID` and `NewRequestID`. Failures come back as `*APIError` that matches
`ErrNotFound`, `ErrConflict`, `ErrBookTransfer`, `ErrRateLimited`, `ErrUnavailable` and others with `errors.Is`; `pix.Retryable`
tells whether trying again can work. The client does not retry: that depends on the payment flow.

## SPI client

`NewSPIClient(baseURL, ispb)` in `internal/pix`: `Send` (1 to 10 messages), `Pull` (reads the stream; pass `""` to open it, then
the returned next path) and `Ack` (closes the stream at a next path). The messages of the payment cycle: `NewPacs008` and
`ParsePacs008` (the payment order), `NewPacs002` and `ParsePacs002` (the status), `ParseAdmi002` (the SPI could not process a
message) and `MessageKind` to tell what came from the stream. Returns (`pacs.004`) and reports (`trck.002`) are not built yet.
Like the DICT client, it does not retry; a message sent again is safe, since the SPI is idempotent by `EndToEndId`.

## Test accounts

`make up` loads three accounts, with the same people, branch and account numbers as the wallet's accounts in the DICT
seed. Their IDs are fixed, so they can be used by hand:

| ID | Holder | Account | Balance |
|----|--------|---------|---------|
| `0a1b2c3d-0001-4000-8000-000000000001` | Ana Souza | `0001` / `00123456` | R$ 1.000,00 |
| `0a1b2c3d-0002-4000-8000-000000000002` | Bruno Lima | `0001` / `00234567` | R$ 500,00 |
| `0a1b2c3d-0003-4000-8000-000000000003` | Padaria Pão Quente Ltda | `0001` / `00345678` | R$ 0,00 |

With the API running, `GetBalance` of Ana (for example with `grpcurl`, or a client generated from `gen/`) returns
`available_cents: 100000`.

## Tests

```sh
go test ./...           # unit tests; the ones that need PostgreSQL are skipped
make test-integration   # all of them, on the wallet's PostgreSQL (needs Docker); each test gets its own schema
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
