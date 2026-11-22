# DICT mock

The Bacen **DICT** (Pix key directory), following the [DICT API v2.13.0](docs/openapi.json): same paths, XML bodies, field
names, validation and `problem+xml` errors. Spec taken from the Bacen site on 2026-11-11
(https://www.bcb.gov.br/content/estabilidadefinanceira/pix/API-DICT.html). The base path is `/api/v2`, as in the real one.

## Running

```sh
make run               # Postgres (docker compose) + the mock on :8080
make seed              # test keys (idempotent)
make test              # unit, no database
make test-integration  # everything, on Postgres
make db-down
```

| Variable | Default |
|----------|---------|
| `DICT_ADDR` | `:8080` |
| `DICT_DATABASE_URL` | `postgres://dict:dict@localhost:5432/dict?sslmode=disable` |
| `DICT_CONTROL` | on; `off` removes `/mock` |

Migrations run at start; `GET /healthz` pings the database. `make setup` at the repo root starts both mocks.

## Seed data

Fixed keys from [`internal/seed/data.go`](internal/seed/data.go), fictional ISPBs, CPFs/CNPJs with valid check digits.
`make seed` never overwrites; `docker compose down -v` to start over. A test fails if a key is missing here.

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

Accounts `00900001`–`00900011` are for the [SPI mock](../spi/README.md#seed-data) scenarios: closed, blocked, never
answers, then one rejection reason each (`DS04`, `FRAD`, `AM02`, `AG03`, `ED05`, `AB09`, `AC14`, `CH11`). Ana and Bruno
are both wallet customers, so paying between them is a book transfer.

```sh
curl -s -H "X-Participant-ISPB: 99000001" -H "PI-RequestingParticipant: 99000001" \
  -H "PI-PayerId: 52998224725" -H "PI-EndToEndId: E9900000120261111120000000000001" \
  "http://localhost:8080/api/v2/entries/%2B5521999990003"      # Carlos at Banco Alfa
```

## Behavior

- **Caller.** No mTLS: every `/api/v2` request needs `X-Participant-ISPB` (`403` otherwise). Writes must come from the
  participant that owns the entry; reads are open.
- **Book transfer.** `GET /entries/{Key}` for a key held by `PI-RequestingParticipant` is `400`
  `EntryCannotBeQueriedForBookTransfer`, as in the real DICT.
- **`createEntry`** is idempotent per (participant, `RequestId`); other data is `RequestIdAlreadyUsed`. EVP keys are
  generated here, so `Key` must be omitted. `SLRY` accounts are `AccountTypeInvalid`.
- **`getEntry`** needs `PI-RequestingParticipant`, `PI-PayerId` and `PI-EndToEndId`. The `EndToEndId` shape is checked
  against the SPI pattern, stricter than the OpenAPI file (which only says "string").
- **`checkKeys`** takes 1–200 keys and keeps their order.

| Operation | Path | Success |
|-----------|------|---------|
| `createEntry` | `POST /entries` | 201 |
| `getEntry` | `GET /entries/{Key}` | 200 |
| `updateEntry` | `PUT /entries/{Key}` | 200 |
| `deleteEntry` | `POST /entries/{Key}/delete` | 200 |
| `checkKeys` | `POST /keys/check` | 200 |

Everything else (claims, funds recovery, infraction reports, refunds, fraud markers, statistics, notifications,
policies, CID) returns `404`.

## Control API

JSON under `/mock`, not part of Bacen's contract.

| | |
|-|-|
| `GET /mock/entries[?participant=&limit=]` | registered keys |
| `POST /mock/reset` | back to the seed, faults cleared |
| `POST /mock/faults`, `GET`, `DELETE /mock/faults[/{id}]` | answer `429`/`503` (with `Retry-After`) or late |

```sh
curl -s -X POST localhost:8080/mock/faults -d '{"method":"GET","status":429,"retry_after":5,"count":3}'
```

A rule is `{method, path_prefix, status, retry_after, delay_ms, count}` and never touches `/mock` or `/healthz`.

## Contract tests

`internal/contract` checks the mock against `docs/openapi.json` with a small validator of its own: the spec's examples
follow its schemas, the mock accepts those requests, and every status and body it returns is one the spec documents.
Known gap: the spec documents no `400` for `checkKeys`, which the mock returns for more than 200 keys.

## Deliberate simplifications and mock decisions

- No mTLS or XML signatures (`<Signature>` is empty).
- No rate limiting of its own; statistics are always zero; no claims.
- A key already registered is `409 ResourceConflict`; an unexpected error is `503` with `Retry-After` (the spec has no `500`).
- Deleting removes the row, so its `RequestId` can be reused. Check digits are not verified.

## Layout

```
cmd/dict, cmd/seed
internal/dict        domain: entities, validation, service
internal/httpapi     handlers, XML, problem+xml
internal/control     /mock
internal/fault       429/503/delay rules
internal/contract    tests against docs/openapi.json
internal/postgres    repository + migrations (pgtest: per-test schema)
internal/memstore    in-memory repository, tests only
internal/e2e, internal/seed, internal/config
```
