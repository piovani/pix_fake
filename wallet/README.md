# Wallet

The digital wallet and its Pix integration layer: accounts, balances, a ledger, and the participant side of the DICT and
the SPI. It is the product of this project; the mocks next to it (`../mock`) only stand in for Bacen.

> **Status: not started.** The stack is not chosen yet (see the [roadmap](../docs/roadmap.md#phase-3-wallet-and-pix-integration-layer-next)),
> so there is no wallet code. What exists is the independent base below.

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

## Running the base

```sh
make up          # from the repository root: both mocks and the wallet's database, everything
```

Or only the wallet's side, from `wallet/`: `cp .env.example .env` and `make up` (PostgreSQL on `:5434`). To stop it or to
start with an empty database: `docker compose down` or `docker compose down -v` inside `wallet/`.

The mocks alone are `make setup` at the root (DICT on `:8080`, SPI on `:8081`). How to call them is in the
[integration guide](../docs/integration-guide.md).

## Ports

| Port | Who |
|------|-----|
| 8080 | DICT mock |
| 8081 | SPI mock |
| 5432, 5433 | PostgreSQL of the DICT and SPI mocks |
| 5434 | PostgreSQL of the wallet |
