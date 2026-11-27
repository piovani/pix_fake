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
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty: no telemetry. Set, it sends traces, metrics and logs there |

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
- **Reconciliation.** Every entry has a CID (`hmacSha256` keyed by the `RequestId` that created it, over the key, owner
  and account, as the spec's Reconciliation section says), and every write records a CID event (`ADDED`, `REMOVED`, both
  for an update) in the same transaction. `createSyncVerification` compares the caller's VSync (XOR of its CIDs of one
  key type) with the DICT's. `createCidSetFile` answers `REQUESTED`, as the spec's asynchronous DICT does, but the file
  is ready at once: `getCidSetFile` gives `AVAILABLE` with a `Url` under `/files/cids/{Id}` on the mock itself, which
  stands for the DICT's storage and asks for no participant. `listCidSetEvents` gives `SyncVerifierStart`/`End` from the
  events themselves. A participant only reconciles its own entries (`403` otherwise).

| Operation | Path | Success |
|-----------|------|---------|
| `createEntry` | `POST /entries` | 201 |
| `getEntry` | `GET /entries/{Key}` | 200 |
| `updateEntry` | `PUT /entries/{Key}` | 200 |
| `deleteEntry` | `POST /entries/{Key}/delete` | 200 |
| `checkKeys` | `POST /keys/check` | 200 |
| `createSyncVerification` | `POST /sync-verifications` | 201 |
| `createCidSetFile` | `POST /cids/files` | 201 |
| `getCidSetFile` | `GET /cids/files/{Id}` | 200 |
| `listCidSetEvents` | `GET /cids/events` | 200 |
| `getEntryByCid` | `GET /cids/entries/{Cid}` | 200 |
| `createInfractionReport` | `POST /infraction-reports` | 201 |
| `listInfractionReport` | `GET /infraction-reports` | 200 |
| `getInfractionReport` | `GET /infraction-reports/{InfractionReportId}` | 200 |
| `updateInfractionReport` | `PUT /infraction-reports/{InfractionReportId}` | 200 |
| `acknowledgeInfractionReport` | `POST /infraction-reports/{InfractionReportId}/acknowledge` | 200 |
| `cancelInfractionReport` | `POST /infraction-reports/{InfractionReportId}/cancel` | 200 |
| `closeInfractionReport` | `POST /infraction-reports/{InfractionReportId}/close` | 200 |
| `createFraudMarker` | `POST /fraud-markers` | 201 |
| `listFraudMarkers` | `GET /fraud-markers` | 200 |
| `getFraudMarker` | `GET /fraud-markers/{FraudMarkerId}` | 200 |
| `cancelFraudMarker` | `POST /fraud-markers/{FraudMarkerId}/cancel` | 200 |
| `createRefund` | `POST /refunds` | 201 |
| `listRefund` | `GET /refunds` | 200 |
| `getRefund` | `GET /refunds/{RefundId}` | 200 |
| `cancelRefund` | `POST /refunds/{RefundId}/cancel` | 200 |
| `closeRefund` | `POST /refunds/{RefundId}/close` | 200 |
| `getPersonStatistics` | `GET /persons/{TaxIdNumber}/statistics` | 200 |
| `getEntryStatistics` | `GET /entries/{Key}/statistics` | 200 |
| `createFundsRecovery` | `POST /funds-recoveries` | 201 |
| `getFundsRecovery` | `GET /funds-recoveries/{FundsRecoveryId}` | 200 |
| `updateFundsRecovery` | `PUT /funds-recoveries/{FundsRecoveryId}` | 200 |
| `createTrackingGraph` | `POST /funds-recoveries/{FundsRecoveryId}/tracking-graph` | 410 (deprecated) |
| `getTrackingGraph` | `GET /funds-recoveries/{FundsRecoveryId}/tracking-graph` | 200 |
| `blockFundsRecovery` | `POST /funds-recoveries/{FundsRecoveryId}/block` | 410 (deprecated) |
| `listFundsRecoveryInfractionReports` | `GET /funds-recoveries/{FundsRecoveryId}/infraction-reports` | 200 |
| `refundFundsRecovery` | `POST /funds-recoveries/{FundsRecoveryId}/refund` | 200 |
| `listFundsRecoveryRefunds` | `GET /funds-recoveries/{FundsRecoveryId}/refunds` | 200 |
| `cancelFundsRecovery` | `POST /funds-recoveries/{FundsRecoveryId}/cancel` | 200 |
| `listEventNotifications` | `GET /event-notifications` | 200 |

Everything else (claims, policies) returns `404`.

### MED: infraction reports, fraud markers, refunds, funds recovery

The real DICT knows every transaction settled in the SPI. The mock only knows the ones it is told about through
`POST /mock/transactions` (below): a report or refund on any other `TransactionId` is `400`.

- **Infraction reports.** `REFUND_REQUEST` is opened by the payer's participant, `REFUND_CANCELLED` by the payee's
  (with `SituationType` `OTHER`); the other one is the counterparty. One report per transaction and reason can be
  `OPEN` or `ACKNOWLEDGED` at a time (`409`). The counterparty acknowledges (repeating it is harmless) and closes an
  `ACKNOWLEDGED` report; closing again with the same analysis is harmless, with another one `409`. `AGREED` needs a
  `FraudType` (`OTHER` also needs `AnalysisDetails`) and creates a fraud marker on the user the report is about (the
  payee; the payer for `REFUND_CANCELLED`), with the paid key, registered by the counterparty. The reporter updates
  while `OPEN`/`ACKNOWLEDGED` and cancels at any moment; cancelling a report closed with `AGREED` cancels its marker.
  A participant that is not part of a report gets `404`, one that is but has the wrong role `403`.
  `InfractionAmount` is the amount of the transaction. `UNKNOWN` (API v1) is never accepted. A report opened by a
  funds recovery is updated and cancelled through it (`400 InfractionReportOperationInvalid` otherwise).
- **Fraud markers.** Created by a participant on its own user, idempotent per `RequestId`; a `Key` registered to
  someone else is `400`. Visible to the registrant and to the reporter of the report it came from; only the
  registrant cancels. `listFraudMarkers` lists the caller's own markers: `Limit` cuts the set, `page.*` pages it.
- **Refunds.** Asked by the payer's participant. `FRAUD` needs a `REFUND_REQUEST` report of the caller on the
  transaction closed with `AGREED`; `OPERATIONAL_FLAW` and `PIX_AUTOMATICO` need `RefundDetails`. One open refund
  per transaction (`409`), and the open and accepted ones never ask for more than its amount. The contested
  participant closes: accepted with the `RefundTransactionId` of the return it made in the SPI (`pacs.004`),
  rejected with a `RefundRejectionReason` (`INVALID_REQUEST` only for `OPERATIONAL_FLAW`). The requesting one
  cancels while open, unless a funds recovery asked for it (`400 RefundOperationInvalid`). `listRefund` pages with an
  opaque `Cursor`. A report of a funds recovery does not open the way to a `FRAUD` refund of its own: the funds
  recovery asks for it.
- **Funds recovery.** Only the `AUTOMATED` flow, the one the spec keeps. The payer's participant of the root
  transaction creates it, once per root ever (`FundsRecoveryForTransactionAlreadyExists`, cancelled or not), and in
  the same request the DICT draws the tracking graph and opens a `REFUND_REQUEST` report on each transaction of it, so
  the answer is already `AWAITING_ANALYSIS`, with the graph. The graph follows the money breadth first: out of the
  account that received it, the transactions made within `HopWindow` after it came in, from `MinTransactionAmount`
  up, until `MaxHops` deep or `MaxTransactions` in all. An account is a user at a participant (the DICT only knows
  those of a transaction); users get pseudo ids, natural or legal by the length of the tax id. What is blocked
  (`InfractionAmount`) on each transaction is no more than what came into the account, shared among the transactions
  out of it in the order they were made; a transaction with nothing to block, or with a report already in progress,
  gets no report. When the last report is closed it is `ANALYSED`; `refund` then asks for a `FRAUD` refund on each
  transaction agreed, of what was blocked and is still refundable (`REFUNDING`, or `COMPLETED` at once with none), and
  it is `COMPLETED` when the last refund is closed. The creator updates it until analysed (its open reports too) and
  cancels it until completed, which cancels every report (with their markers) and every open refund. The creator and
  the counterparties of its reports see it and its graph (`404` otherwise); its lists are the creator's (`403`).
  `createTrackingGraph` and `blockFundsRecovery`, of the deprecated `INTERACTIVE` flow, are `410 DeprecatedResource`.
- **Graph parameters.** The spec gives no bounds; the mock's are below. A value out of them is `400
  FundsRecoveryTrackingGraphParameterInvalid` with `minParameterValue` and `maxParameterValue`.

  | Parameter | Default | Range |
  |-----------|---------|-------|
  | `MinTransactionAmount` | `0.00` | `0.00`–`1000000.00` |
  | `MaxTransactions` | `500` | 1–1000 |
  | `HopWindow` | `PT24H` | `PT1H`–`PT168H` |
  | `MaxHops` | `5` | 1–10 |
- **Event notifications.** `FUNDS_RECOVERY_ANALYSED` and `FUNDS_RECOVERY_COMPLETED` go to the creator;
  `FUNDS_RECOVERY_INFORMATION_UPDATED` and `FUNDS_RECOVERY_CANCELLED` to the counterparties of its reports.
  `listEventNotifications` gives the caller's events of the last 7 days, 200 a page, with an opaque `Cursor`.
- **Statistics** are counted from what the mock knows, in the windows of the spec (`d90`, `m12`, `m60`, in BRT):
  settlements as payee, fraud markers while `REGISTERED` by type, their transactions' total and distinct
  registrants, open and rejected (`DISAGREED`) reports, and the accounts of the user's keys. `getEntry` with
  `IncludeStatistics=true` gives the same numbers.

The file documents no `400` for `updateInfractionReport`, `closeInfractionReport` and `closeRefund`, though their
bodies can be invalid, and no `403` for the reads, though they need `PI-RequestingParticipant`: the mock answers
both, and the contract tests list them as known gaps. So are the `400` of `updateFundsRecovery` and
`refundFundsRecovery` (their errors are 400s), the `404` of `cancelFundsRecovery`, the `403` of its reads and the
`410` of its deprecated operations. Slips of the file are tolerated there too: the `Total` of
`ListFraudMarkersResponse` is required but not declared, the refund examples write `<TaxId>` for `<TaxIdNumber>`, the
funds recovery examples leave out the required `ContactInformation`, and the block example has a status (`BLOCKING`)
the enumeration no longer has.

## Control API

JSON under `/mock`, not part of Bacen's contract.

| | |
|-|-|
| `GET /mock/entries[?participant=&limit=]` | registered keys, with their CID |
| `POST /mock/reset` | back to the seed, faults cleared |
| `POST /mock/faults`, `GET`, `DELETE /mock/faults[/{id}]` | answer `429`/`503` (with `Retry-After`) or late |
| `POST /mock/transactions`, `GET` | the transactions settled in the SPI that the DICT knows, for the MED |

```sh
curl -s -X POST localhost:8080/mock/faults -d '{"method":"GET","status":429,"retry_after":5,"count":3}'
```

A rule is `{method, path_prefix, status, retry_after, delay_ms, count}` and never touches `/mock` or `/healthz`.

```sh
curl -s -X POST localhost:8080/mock/transactions -d '{"end_to_end_id":"E9900000120261120100000000000001",
  "amount":"150.00","debtor":{"participant":"99000001","tax_id":"52998224725"},
  "creditor":{"participant":"99000002","tax_id":"39053344705","key":"+5521999990003"}}'
```

`settled_at` defaults to now. Adding the same `end_to_end_id` again replaces it; `POST /mock/reset` removes them.

## Contract tests

`internal/contract` checks the mock against `docs/openapi.json` with a small validator of its own: the spec's examples
follow its schemas, the mock accepts those requests, and every status and body it returns is one the spec documents.
Known gap: the spec documents no `400` for `checkKeys`, which the mock returns for more than 200 keys.

## Deliberate simplifications and mock decisions

- No mTLS or XML signatures (`<Signature>` is empty).
- No rate limiting of its own; no claims. Statistics count only what the mock knows (see MED).
- MED: no `EffectiveRefundedAmount`, `RefundAccount` or `MonitorAccount`; a refund closed as accepted is not checked
  against the SPI. A funds recovery is tracked and blocked synchronously, never times out
  (`FundsRecoveryTrackingTimeout`), and does not check the root's age or purpose (`IPRT`). The graph has no account
  opening or company creation dates. No indirect participants: `IncludeIndirectParticipants` is accepted and ignored.
- A key already registered is `409 ResourceConflict`; an unexpected error is `503` with `Retry-After` (the spec has no `500`).
- Deleting removes the row, so its `RequestId` can be reused. Check digits are not verified.
- The wallet's seed keys are created with the `RequestId`s the wallet's own seed uses (`dict.SeedRequestID`), and the
  wallet's Padaria has no trade name, as the wallet registers keys: so the CIDs of both sides agree from the start.
  Migration `002` brings a database seeded before in line, and gives CIDs to the entries it already had.

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
