# DICT mock

Mock of the Central Bank of Brazil (Bacen) **DICT**, the Pix key directory. It follows the official
[DICT API v2.13.0](docs/openapi.json): same paths, XML request/response bodies, field names, validation
rules and `problem+xml` error format. Security is intentionally simplified (see below).

Official spec page: https://www.bcb.gov.br/content/estabilidadefinanceira/pix/API-DICT.html

> **Spec reference:** this mock was built from the DICT API **v2.13.0** (`docs/openapi.json`),
> as available on the Bacen site on **2026-11-11 at 17:24 (UTC-3, Brasília)**.
> If Bacen publishes a newer version after that date, contracts here may differ from the real service.

## Running

Requires Go 1.27.3+ and Docker (for Postgres).

```sh
make run               # starts Postgres (docker compose) and the mock on :8080
make seed              # loads the test keys into the database (idempotent)
make test              # unit tests, no database needed
make test-integration  # everything, including the Postgres repository and end-to-end tests
```

| Variable            | Default                                                        |
|---------------------|----------------------------------------------------------------|
| `DICT_ADDR`         | `:8080`                                                        |
| `DICT_DATABASE_URL` | `postgres://dict:dict@localhost:5432/dict?sslmode=disable`     |

Migrations in `internal/postgres/migrations/` are applied on startup. `GET /healthz` pings the database.

The base path is `/api/v2`, the same as the real service, so pointing the wallet at the real DICT later is
a base URL change.

## Seed data

`make seed` starts Postgres if needed, applies the migrations and registers a fixed set of keys, so the DICT
already answers lookups for tests. It is **idempotent**: running it again changes nothing and reports what already
existed, so it is safe inside a future `make setup`. Existing entries are never overwritten; to start from scratch
run `docker compose down -v` first.

The data lives in [`internal/seed/data.go`](internal/seed/data.go). The institutions use fictional ISPBs, and CPFs
and CNPJs have valid check digits (the mock itself does not verify them). Keys are fixed, including the `EVP` ones,
so tests can rely on them.

| Participant | ISPB | Owner | Account | Keys |
|-------------|------|-------|---------|------|
| Wallet | `99000001` | Ana Souza (CPF `52998224725`) | `0001` / `00123456`, `CACC` | CPF `52998224725`, PHONE `+5511999990001`, EMAIL `ana.souza@example.com`, EVP `8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01` |
| Wallet | `99000001` | Bruno Lima (CPF `11144477735`) | `0001` / `00234567`, `SVGS` | CPF `11144477735`, PHONE `+5511999990002` |
| Wallet | `99000001` | Padaria Pão Quente Ltda (CNPJ `11222333000181`) | `0001` / `00345678`, `CACC` | CNPJ `11222333000181`, EMAIL `contato@paoquente.example.com` |
| Banco Alfa | `99000002` | Carlos Mendes (CPF `39053344705`) | `0001` / `00456789`, `CACC` | CPF `39053344705`, PHONE `+5521999990003`, EVP `2b9d4e17-a3c8-4f65-8d02-7e1c6a0b5d93` |
| Banco Beta | `99000003` | Daniela Rocha (CPF `16899535009`) | `0001` / `00567890`, `TRAN` | CPF `16899535009`, EMAIL `daniela.rocha@example.com` |
| Banco Beta | `99000003` | Mercadinho do Zé ME (CNPJ `45678912000155`) | `0001` / `00678901`, `CACC` | CNPJ `45678912000155`, PHONE `+5531999990004` |

It covers every key type, natural and legal persons, and payments inside the wallet (Ana to Bruno) as well as
across institutions (wallet to Banco Alfa or Beta, and back). A test fails if a seeded key is missing from this
table.

## Identifying the institution

There is no mTLS. Every request to `/api/v2/*` must carry the participant's ISPB:

```
X-Participant-ISPB: 12345678
```

A missing or malformed header returns `403 Forbidden`. It is used for simple authorization:

- `POST /entries`: `Entry/Account/Participant` must equal the header.
- `PUT /entries/{Key}` and `POST /entries/{Key}/delete`: the entry must belong to the caller (and, for delete,
  `Participant` in the body must be the caller), otherwise `403`.
- Reads (`GET /entries/{Key}`, `POST /keys/check`) are open to any identified participant.

## Endpoints

### Implemented

| Operation       | Method and path                      | Success |
|-----------------|--------------------------------------|---------|
| `createEntry`   | `POST /entries`                      | 201     |
| `getEntry`      | `GET /entries/{Key}`                 | 200     |
| `updateEntry`   | `PUT /entries/{Key}`                 | 200     |
| `deleteEntry`   | `POST /entries/{Key}/delete`         | 200     |
| `checkKeys`     | `POST /keys/check`                   | 200     |

### Not implemented yet

| Area              | Endpoints |
|-------------------|-----------|
| Claims            | `POST /claims`, `GET /claims`, `GET /claims/{ClaimId}`, `POST /claims/{ClaimId}/acknowledge`, `/confirm`, `/cancel`, `/complete` |
| Funds recovery    | `POST /funds-recoveries`, `GET` and `PUT /funds-recoveries/{FundsRecoveryId}`, `POST` and `GET .../tracking-graph`, `POST .../block`, `GET .../infraction-reports`, `POST .../refund`, `GET .../refunds`, `POST .../cancel` |
| Infraction reports | `POST` and `GET /infraction-reports`, `GET` and `PUT /infraction-reports/{InfractionReportId}`, `POST .../acknowledge`, `/cancel`, `/close` |
| Refunds           | `POST` and `GET /refunds`, `GET /refunds/{RefundId}`, `POST .../cancel`, `/close` |
| Fraud markers     | `POST` and `GET /fraud-markers`, `GET /fraud-markers/{FraudMarkerId}`, `POST .../cancel` |
| Statistics        | `GET /persons/{TaxIdNumber}/statistics`, `GET /entries/{Key}/statistics` |
| Event notifications | `GET /event-notifications` |
| Policies (rate-limit buckets) | `GET /policies`, `GET /policies/{Policy}` |
| Reconciliation (CID) | `POST /sync-verifications`, `POST /cids/files`, `GET /cids/files/{Id}`, `GET /cids/events`, `GET /cids/entries/{Cid}` |

Unimplemented paths return `404 problem+xml`.

## What follows the spec

- XML bodies with the spec's element names; dates in UTC with milliseconds; `CorrelationId` (32 hex chars)
  in every response and `problem`.
- Key formats per type (`CPF`, `CNPJ`, `PHONE`, `EMAIL`, `EVP`) using the spec's regular expressions;
  `CPF`/`CNPJ` keys must belong to the owner with the same tax id.
- `EVP` keys are generated by the DICT: the request must omit `Key`.
- `createEntry` is idempotent per (participant, `RequestId`): same parameters replay the original `201`;
  different parameters return `RequestIdAlreadyUsed`. `SLRY` accounts return `AccountTypeInvalid`.
- `updateEntry` only changes account data, `Name` and `TradeName`; allowed `Reason` depends on the key type.
- `getEntry` requires the `PI-RequestingParticipant`, `PI-PayerId` and `PI-EndToEndId` headers and honors
  `IncludeStatistics`.
- `checkKeys` accepts 1 to 200 keys and keeps their order.
- Errors use `application/problem+xml` (RFC 7807) with `violations` for invalid fields.

## Deliberate simplifications and mock decisions

- **No mTLS, no XML digital signatures.** `Signature` in requests is ignored; responses carry an empty `<Signature>`.
- **No rate limiting** (`429` is never returned) and no policy buckets.
- **Statistics are always zero.** `IncludeStatistics=true` returns well-formed counters with zeros.
- **No claims/portability**, so `OpenClaimCreationDate` is never present.
- **Key already registered** returns `409 ResourceConflict` (the spec has no dedicated error for it on create).
- **Unexpected errors** (for example the database being down) return `503 ServiceUnavailable` with `Retry-After`,
  since the spec defines no `500`.
- Deleting an entry removes the row, so its `RequestId` can be reused afterwards.
- Check digits of CPF/CNPJ are not verified, so test data can be arbitrary.

## Layout

```
mock/dict/
├── cmd/dict/              # main: config, wiring, graceful shutdown
├── cmd/seed/              # loads the test keys (make seed)
├── internal/
│   ├── dict/              # domain: entities, validation, service, Repository interface
│   ├── httpapi/           # HTTP handlers, XML types, problem+xml
│   ├── postgres/          # Repository on Postgres + embedded migrations
│   │   └── pgtest/        # per-test Postgres schema for integration tests
│   ├── memstore/          # in-memory Repository, tests only
│   ├── e2e/               # end-to-end tests: real HTTP API on Postgres
│   ├── seed/              # the seed data (keys, owners, institutions)
│   └── config/
├── docs/openapi.json      # Bacen DICT API v2.13.0 (source of truth)
├── docker-compose.yml
└── Makefile
```
