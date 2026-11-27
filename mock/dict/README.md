# DICT mock

The Bacen **DICT** (Pix key directory and MED), following the [DICT API v2.13.0](docs/openapi.json): same paths, XML
bodies, field names, validation and `problem+xml` errors, under `/api/v2`. Spec taken from the Bacen site on
2026-11-11 (https://www.bcb.gov.br/content/estabilidadefinanceira/pix/API-DICT.html).

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
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty: no telemetry |

Migrations run at start; `GET /healthz` pings the database. `make setup` at the repo root starts both mocks.

## Seed data

Fixed keys from [`internal/seed/data.go`](internal/seed/data.go): fictional ISPBs, CPFs/CNPJs with valid check digits.
`make seed` never overwrites; `docker compose down -v` starts over.

| Participant | Owner | Account | Keys |
|-------------|-------|---------|------|
| Wallet `99000001` | Ana Souza | `0001` / `00123456`, `CACC` | `52998224725`, `+5511999990001`, `ana.souza@example.com`, `8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01` |
| Wallet `99000001` | Bruno Lima | `0001` / `00234567`, `SVGS` | `11144477735`, `+5511999990002` |
| Wallet `99000001` | Padaria Pão Quente Ltda | `0001` / `00345678`, `CACC` | `11222333000181`, `contato@paoquente.example.com` |
| Banco Alfa `99000002` | Carlos Mendes | `0001` / `00456789`, `CACC` | `39053344705`, `+5521999990003`, `2b9d4e17-a3c8-4f65-8d02-7e1c6a0b5d93` |
| Banco Beta `99000003` | Daniela Rocha | `0001` / `00567890`, `TRAN` | `16899535009`, `daniela.rocha@example.com` |
| Banco Beta `99000003` | Mercadinho do Zé ME | `0001` / `00678901`, `CACC` | `45678912000155`, `+5531999990004` |
| Banco Alfa `99000002` | Eduardo Teixeira | `0001` / `00900001`, `CACC` | `24681357928`, `+5521999990005` |
| Banco Alfa `99000002` | Fernanda Alves | `0001` / `00900002`, `SVGS` | `13579246828`, `+5521999990006` |
| Banco Beta `99000003` | Gustavo Pires | `0001` / `00900003`, `TRAN` | `86420975310`, `+5531999990007` |
| Banco Alfa `99000002` | Helena Moraes | `0001` / `00900004`, `CACC` | `10293847541` |
| Banco Beta `99000003` | Igor Batista | `0001` / `00900005`, `SVGS` | `56473829164` |
| Banco Alfa `99000002` | Julia Nogueira | `0001` / `00900006`, `TRAN` | `91827364564` |
| Banco Beta `99000003` | Kleber Duarte | `0001` / `00900007`, `SVGS` | `37281946546` |
| Banco Alfa `99000002` | Larissa Prado | `0001` / `00900008`, `CACC` | `64528193728` |
| Banco Beta `99000003` | Marcos Vieira | `0001` / `00900009`, `TRAN` | `82910476340` |
| Banco Alfa `99000002` | Natalia Freitas | `0001` / `00900010`, `CACC` | `45197286300` |
| Banco Beta `99000003` | Otavio Ramos | `0001` / `00900011`, `SVGS` | `73640812913` |

The first key of each owner is their CPF or CNPJ. Accounts `00900001`–`00900011` are SPI scenarios (closed, blocked,
never answers, one rejection reason each): see the [test data of the integration guide](../../docs/integration-guide.md#test-data).
A test fails if a seeded key is missing from this table.

```sh
curl -s -H "X-Participant-ISPB: 99000001" -H "PI-RequestingParticipant: 99000001" \
  -H "PI-PayerId: 52998224725" -H "PI-EndToEndId: E9900000120261111120000000000001" \
  "http://localhost:8080/api/v2/entries/%2B5521999990003"      # Carlos at Banco Alfa
```

## Operations

| Area | Operations |
|------|------------|
| Entries | `createEntry` `POST /entries` · `getEntry` `GET /entries/{Key}` · `updateEntry` `PUT /entries/{Key}` · `deleteEntry` `POST /entries/{Key}/delete` · `checkKeys` `POST /keys/check` |
| Reconciliation | `createSyncVerification` `POST /sync-verifications` · `createCidSetFile` `POST /cids/files` · `getCidSetFile` `GET /cids/files/{Id}` · `listCidSetEvents` `GET /cids/events` · `getEntryByCid` `GET /cids/entries/{Cid}` |
| Infraction reports | `POST /infraction-reports`, `GET` (list), `GET /{Id}`, `PUT /{Id}`, `POST /{Id}/acknowledge`, `/cancel`, `/close` |
| Fraud markers | `POST /fraud-markers`, `GET` (list), `GET /{Id}`, `POST /{Id}/cancel` |
| Refunds | `POST /refunds`, `GET` (list), `GET /{Id}`, `POST /{Id}/cancel`, `/close` |
| Statistics | `GET /persons/{TaxIdNumber}/statistics`, `GET /entries/{Key}/statistics` |
| Funds recovery | `POST /funds-recoveries`, `GET /{Id}`, `PUT /{Id}`, `GET /{Id}/tracking-graph`, `GET /{Id}/infraction-reports`, `POST /{Id}/refund`, `GET /{Id}/refunds`, `POST /{Id}/cancel`; `createTrackingGraph` and `blockFundsRecovery` are `410` (deprecated) |
| Events | `listEventNotifications` `GET /event-notifications` |

Creates answer `201`, everything else `200`. Claims and policies are `404`.

## Behavior

- **Caller.** Every request needs `X-Participant-ISPB` (`403` otherwise). Writes come from the participant that owns
  the entry; reads are open. MED calls also need `PI-RequestingParticipant`.
- **`createEntry`** is idempotent per (participant, `RequestId`); other data is `RequestIdAlreadyUsed`. EVP keys are
  generated here (`Key` omitted). `SLRY` accounts are `AccountTypeInvalid`.
- **`getEntry`** needs `PI-RequestingParticipant`, `PI-PayerId` and `PI-EndToEndId`. A key of the requesting
  participant is `400 EntryCannotBeQueriedForBookTransfer`.
- **`checkKeys`** takes 1–200 keys and keeps their order.
- **Reconciliation.** Every entry has a CID (`hmacSha256` keyed by the `RequestId` that created it) and every write
  records a CID event in the same transaction. `createSyncVerification` compares the caller's VSync with the DICT's.
  `createCidSetFile` answers `REQUESTED`, but the file is ready at once: `getCidSetFile` gives `AVAILABLE` with a `Url`
  under `/files/cids/{Id}` on the mock itself. A participant reconciles only its own entries.

## MED

The DICT knows the transactions the SPI mock settles (it calls `POST /mock/transactions` when `SPI_DICT_URL` points
here, as in `make up`); a report or refund on any other `TransactionId` is `400`. A participant outside a report or
refund gets `404`, one with the wrong role `403`.

**Infraction reports**
- `REFUND_REQUEST` is opened by the payer's participant, `REFUND_CANCELLED` by the payee's (situation `OTHER`). One per
  transaction and reason can be `OPEN` or `ACKNOWLEDGED` (`409`). `UNKNOWN` (API v1) is never accepted.
- The counterparty acknowledges, then closes. `AGREED` needs a `FraudType` and creates a fraud marker on the user
  reported, with the paid key. Repeating an acknowledge or the same close is harmless; another close is `409`.
- The reporter updates while open and cancels at any moment; cancelling an `AGREED` report cancels its marker.
- `InfractionAmount` is the transaction's amount (or what a funds recovery blocks).

**Fraud markers** are created by a participant on its own user, idempotent per `RequestId`. The registrant and the
reporter of their report see them; only the registrant cancels.

**Refunds**
- Asked by the payer's participant: `FRAUD` needs its `REFUND_REQUEST` report closed `AGREED`; `OPERATIONAL_FLAW` and
  `PIX_AUTOMATICO` need `RefundDetails`. One open per transaction (`409`), never above what is left of it.
- The contested participant closes: accepted with the `RefundTransactionId` (the `RtrId` of its `pacs.004`), or rejected
  with a reason (`INVALID_REQUEST` only for `OPERATIONAL_FLAW`). The requester cancels while open.
- `listRefund` pages with an opaque `Cursor`.

**Funds recovery** (only the `AUTOMATED` flow, the one the spec keeps)
- The payer's participant of the root transaction creates it, once per root. In the same request the DICT draws the
  tracking graph and opens a report on each transaction of it: the answer is already `AWAITING_ANALYSIS`.
- The graph follows the money breadth first: out of the account that received it, the transactions made within
  `HopWindow`, from `MinTransactionAmount`, up to `MaxHops` deep and `MaxTransactions` in all. What each report blocks
  is no more than what came into the account.
- When the last report is closed it is `ANALYSED`; `refund` asks for a `FRAUD` refund on each agreed transaction
  (`REFUNDING`), and it is `COMPLETED` when the last refund is closed. Its reports and refunds change only through it.
- The creator updates it until analysed and cancels it until completed (which cancels its reports and open refunds).

| Graph parameter | Default | Range (the spec gives none) |
|-----------------|---------|-----------------------------|
| `MinTransactionAmount` | `0.00` | `0.00`–`1000000.00` |
| `MaxTransactions` | `500` | 1–1000 |
| `HopWindow` | `PT24H` | `PT1H`–`PT168H` |
| `MaxHops` | `5` | 1–10 |

**Event notifications**: `FUNDS_RECOVERY_ANALYSED` and `_COMPLETED` go to the creator, `_INFORMATION_UPDATED` and
`_CANCELLED` to the counterparties of its reports. `listEventNotifications` gives the last 7 days, 200 a page.

**Statistics** count what the mock knows, in the spec's windows (`d90`, `m12`, `m60`, BRT): settlements, fraud markers,
open and rejected reports, and the accounts of the user's keys. `getEntry` with `IncludeStatistics=true` gives the same.

## Control API

JSON under `/mock`, not part of Bacen's contract.

| | |
|-|-|
| `GET /mock/entries[?participant=&limit=]` | registered keys, with their CID |
| `POST /mock/reset` | back to the seed, faults cleared |
| `POST /mock/faults`, `GET`, `DELETE /mock/faults[/{id}]` | answer `429`/`503` (with `Retry-After`) or late |
| `POST /mock/transactions`, `GET` | the settled transactions the DICT knows, for the MED |

```sh
curl -s -X POST localhost:8080/mock/faults -d '{"method":"GET","status":429,"retry_after":5,"count":3}'
curl -s -X POST localhost:8080/mock/transactions -d '{"end_to_end_id":"E9900000120261120100000000000001",
  "amount":"150.00","debtor":{"participant":"99000001","tax_id":"52998224725"},
  "creditor":{"participant":"99000002","tax_id":"39053344705","key":"+5521999990003"}}'
```

A fault is `{method, path_prefix, status, retry_after, delay_ms, count}` and never touches `/mock` or `/healthz`. A
transaction's `settled_at` defaults to now; the same `end_to_end_id` replaces it.

## Contract tests

`internal/contract` checks the mock against `docs/openapi.json`: the spec's examples follow its schemas, the mock
accepts those requests, and every status and body it returns is one the spec documents. Known gaps, where the mock
answers something the file does not document:

- `400` for `checkKeys` (over 200 keys), `updateInfractionReport`, `closeInfractionReport`, `closeRefund`,
  `updateFundsRecovery` and `refundFundsRecovery`;
- `403` for the MED reads (they need `PI-RequestingParticipant`), `404` for `cancelFundsRecovery`, `410` for the
  deprecated funds recovery operations.

Slips of the file that are tolerated: the `Total` of `ListFraudMarkersResponse` is required but not declared, the refund
examples write `<TaxId>` for `<TaxIdNumber>`, the funds recovery examples leave out the required `ContactInformation`,
and the block example has a status (`BLOCKING`) the enumeration no longer has.

## Simplifications

- No mTLS or XML signatures, no rate limiting of its own, no claims. Check digits are not verified.
- A key already registered is `409 ResourceConflict`; an unexpected error is `503` with `Retry-After` (the spec has no
  `500`). Deleting removes the row, so its `RequestId` can be reused.
- MED: no `EffectiveRefundedAmount`, `RefundAccount` or `MonitorAccount`; an accepted refund is not checked against the
  SPI. A funds recovery is tracked synchronously (never `FundsRecoveryTrackingTimeout`), does not check the root's age
  or purpose, and its graph has no account opening dates. `IncludeIndirectParticipants` is accepted and ignored.
- The wallet's seed keys use the `RequestId`s of the wallet's own seed (`dict.SeedRequestID`), so both sides' CIDs agree
  from the start.

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
