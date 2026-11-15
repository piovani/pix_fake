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
make lint              # gofmt and go vet
make db-up             # starts only Postgres
make db-down           # stops Postgres (add -v to docker compose down to drop the data)
```

| Variable            | Default                                                        |
|---------------------|----------------------------------------------------------------|
| `DICT_ADDR`         | `:8080`                                                        |
| `DICT_DATABASE_URL` | `postgres://dict:dict@localhost:5432/dict?sslmode=disable`     |
| `DICT_CONTROL`      | on; `off` removes the control API (see below)                  |

Migrations in `internal/postgres/migrations/` are applied on startup. `GET /healthz` pings the database.

The whole environment (both mocks and their databases) starts with `make setup` at the repository root, from the
[`Dockerfile`](Dockerfile) of this mock; see the [integration guide](../../docs/integration-guide.md).

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
| Banco Alfa | `99000002` | Eduardo Teixeira (CPF `24681357928`) | `0001` / `00900001`, `CACC` | CPF `24681357928`, PHONE `+5521999990005` |
| Banco Alfa | `99000002` | Fernanda Alves (CPF `13579246828`) | `0001` / `00900002`, `SVGS` | CPF `13579246828`, PHONE `+5521999990006` |
| Banco Beta | `99000003` | Gustavo Pires (CPF `86420975310`) | `0001` / `00900003`, `TRAN` | CPF `86420975310`, PHONE `+5531999990007` |
| Banco Alfa | `99000002` | Helena Moraes (CPF `10293847541`) | `0001` / `00900004`, `CACC` | CPF `10293847541` |
| Banco Beta | `99000003` | Igor Batista (CPF `56473829164`) | `0001` / `00900005`, `SVGS` | CPF `56473829164` |
| Banco Alfa | `99000002` | Julia Nogueira (CPF `91827364564`) | `0001` / `00900006`, `TRAN` | CPF `91827364564` |
| Banco Beta | `99000003` | Kleber Duarte (CPF `37281946546`) | `0001` / `00900007`, `SVGS` | CPF `37281946546` |
| Banco Alfa | `99000002` | Larissa Prado (CPF `64528193728`) | `0001` / `00900008`, `CACC` | CPF `64528193728` |
| Banco Beta | `99000003` | Marcos Vieira (CPF `82910476340`) | `0001` / `00900009`, `TRAN` | CPF `82910476340` |
| Banco Alfa | `99000002` | Natalia Freitas (CPF `45197286300`) | `0001` / `00900010`, `CACC` | CPF `45197286300` |
| Banco Beta | `99000003` | Otavio Ramos (CPF `73640812913`) | `0001` / `00900011`, `SVGS` | CPF `73640812913` |

The last eleven accounts (`00900001` to `00900011`) exist for the [SPI mock](../spi/README.md#seed-data): a payment to
`00900001` is rejected as closed, to `00900002` as blocked, `00900003` never answers (timeout), and each of `00900004` to
`00900011` rejects with one reason (`DS04`, `FRAD`, `AM02`, `AG03`, `ED05`, `AB09`, `AC14`, `CH11`, in this order), so a
wallet can try its handling of each. They are in both seeds with the same numbers and owners.

It covers every key type, natural and legal persons, and payments across institutions (wallet to Banco Alfa or
Beta, and back). Ana and Bruno are both wallet customers, so a payment between them is a book transfer that never
queries the DICT (see [Book transfers](#book-transfers)). A test fails if a seeded key is missing from this table.

The wallet's ISPB (`99000001`) is a placeholder: change the constants at the top of `internal/seed/data.go` and the
table above to use the real one.

### Trying it

With the mock running (`make run`) and the seed loaded (`make seed`), look up a key of another institution the way
the wallet would before paying (the `+` of the phone key is URL-encoded as `%2B`):

```sh
curl -s \
  -H "X-Participant-ISPB: 99000001" \
  -H "PI-RequestingParticipant: 99000001" \
  -H "PI-PayerId: 52998224725" \
  -H "PI-EndToEndId: E9900000120261111120000000000001" \
  "http://localhost:8080/api/v2/entries/%2B5521999990003"
```

It returns the owner (Carlos Mendes) and the account at Banco Alfa. A key that is not in the seed returns `404`.

The same lookup for a key the wallet itself holds (Ana's CPF) is refused, as in the real DICT:

```sh
curl -s \
  -H "X-Participant-ISPB: 99000001" \
  -H "PI-RequestingParticipant: 99000001" \
  -H "PI-PayerId: 11144477735" \
  -H "PI-EndToEndId: E9900000120261111120000000000002" \
  "http://localhost:8080/api/v2/entries/52998224725"
```

It returns `400` with the `EntryCannotBeQueriedForBookTransfer` problem.

## Book transfers

When payer and payee are customers of the same participant, the payment is a *book transfer*: it is settled inside
the institution, never reaches the SPI and must not query the DICT. The DICT API v2.13.0 lists the error
`EntryCannotBeQueriedForBookTransfer` for this case, and the mock enforces it: `GET /entries/{Key}` returns `400`
when the entry is held by the participant given in `PI-RequestingParticipant`. A key that does not exist is still a
`404`, and the other operations are not affected.

The participant has to recognize its own keys in its own records, since the DICT will not answer for them.

## Identifying the institution

There is no mTLS. Every request to `/api/v2/*` must carry the participant's ISPB:

```
X-Participant-ISPB: 12345678
```

A missing or malformed header returns `403 Forbidden`. It is used for simple authorization:

- `POST /entries`: `Entry/Account/Participant` must equal the header.
- `PUT /entries/{Key}` and `POST /entries/{Key}/delete`: the entry must belong to the caller (and, for delete,
  `Participant` in the body must be the caller), otherwise `403`.
- Reads (`POST /keys/check`, and `GET /entries/{Key}` except for [book transfers](#book-transfers)) are open to any
  identified participant.

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

Paying and receiving Pix only needs the five operations above. The claims (moving a key that is already registered
at another institution) were left out on purpose and can be added later; the rest belongs to the fraud and
reconciliation side of the ecosystem.

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
  `IncludeStatistics`. It refuses keys held by the requesting participant (book transfer, see above). The OpenAPI file
  only calls `PI-EndToEndId` a string that corresponds to the `EndToEndId` of the `pacs.008`; the mock is stricter and
  checks the shape that the SPI schema requires (`E` + ISPB (8) + `yyyyMMddHHmm` (12) + 11 alphanumeric characters), so a
  participant that builds the identifier wrong finds out at the lookup and not at the payment. The date is not checked
  here (the SPI checks it against its clock).
- `checkKeys` accepts 1 to 200 keys and keeps their order.
- Errors use `application/problem+xml` (RFC 7807) with `violations` for invalid fields.

## Control API and simulated failures

Not part of the Bacen contract: JSON under `/mock`, to help test a client. It is on by default and `DICT_CONTROL=off` removes it.

| | |
|-|-|
| `GET /mock/entries[?participant=&limit=]` | the registered keys, ordered by key |
| `POST /mock/reset` | removes every entry and loads the seed data again; also clears the faults |
| `POST /mock/faults` | makes the API answer `429` or `503` (with `Retry-After`), or answer late, for some requests or until removed |
| `GET /mock/faults`, `DELETE /mock/faults[/{id}]` | list and remove |

```sh
curl -s -X POST localhost:8080/mock/faults -d '{"status":503,"retry_after":2}'                  # every request: 503
curl -s -X POST localhost:8080/mock/faults -d '{"method":"GET","status":429,"retry_after":5,"count":3}'
curl -s -X POST localhost:8080/mock/faults -d '{"delay_ms":8000,"count":1}'                      # one slow answer
```

A rule is `{method, path_prefix, status, retry_after, delay_ms, count}`; it never affects `/mock` or `/healthz`.

## Contract tests

`internal/contract` checks the mock against the Bacen OpenAPI file ([`docs/openapi.json`](docs/openapi.json)) with a small
validator of its own, so there is no dependency: the examples in the file must follow the file's own schemas (which tests
the validator); the mock must accept the requests of those examples; and for each of the five operations and for its
errors (`400`, `403`, `404`, `409`, and the simulated `429` and `503`) the status must be one the file documents for that
operation and the body must follow the schema of that response. A change in the XML that drifts from the specification
fails the build. One known gap is listed in the test: the file limits `Keys` to 1..200 but documents no `400` for
`checkKeys`, and the mock answers `400`.

## Deliberate simplifications and mock decisions

- **No mTLS, no XML digital signatures.** `Signature` in requests is ignored; responses carry an empty `<Signature>`.
- **No rate limiting of its own** (`429` only when a test asks for it, see above) and no policy buckets.
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
│   ├── control/           # the control API under /mock
│   ├── fault/             # the 429/503/delay rules a test can add
│   ├── contract/          # contract tests against docs/openapi.json
│   ├── postgres/          # Repository on Postgres + embedded migrations
│   │   └── pgtest/        # per-test Postgres schema for integration tests
│   ├── memstore/          # in-memory Repository, tests only
│   ├── e2e/               # end-to-end tests: real HTTP API on Postgres
│   ├── seed/              # the seed data (keys, owners, institutions)
│   └── config/
├── docs/openapi.json      # Bacen DICT API v2.13.0 (source of truth)
├── Dockerfile             # the mock as a container (used by the root docker-compose.yml)
├── docker-compose.yml     # only PostgreSQL, for `make run` here
└── Makefile
```
