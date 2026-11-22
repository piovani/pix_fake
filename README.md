# pix_fake

[![CI](https://github.com/piovani/pix_fake/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/piovani/pix_fake/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27-00ADD8?logo=go&logoColor=white)](wallet/go.mod)
[![Coverage](https://img.shields.io/badge/coverage-87%25-brightgreen)](scripts/coverage.sh)
[![Dependabot](https://img.shields.io/badge/dependabot-enabled-025E8C?logo=dependabot&logoColor=white)](.github/dependabot.yml)
[![Last commit](https://img.shields.io/github/last-commit/piovani/pix_fake)](https://github.com/piovani/pix_fake/commits/main)

[![gRPC](https://img.shields.io/badge/gRPC-API-244c5a?logo=grpc&logoColor=white)](wallet/api/proto/wallet/v1)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-17-4169E1?logo=postgresql&logoColor=white)](docker-compose.yml)
[![OpenTelemetry](https://img.shields.io/badge/OpenTelemetry-traces%20%C2%B7%20metrics%20%C2%B7%20logs-425CC7?logo=opentelemetry&logoColor=white)](README.md#monitoring)
[![Grafana](https://img.shields.io/badge/Grafana-dashboards-F46800?logo=grafana&logoColor=white)](monitoring/dashboards)
[![Docker](https://img.shields.io/badge/Docker-compose-2496ED?logo=docker&logoColor=white)](docker-compose.yml)
[![ISO 20022](https://img.shields.io/badge/ISO%2020022-pacs%20%C2%B7%20camt%20%C2%B7%20trck-6a737d)](mock/spi/README.md#messages)

A Pix wallet built against mocks of the Bacen infrastructure. Only regulated participants can reach the real Pix, so
this repo mocks the **DICT** (key directory) and the **SPI** (settlement) with the same contracts Bacen documents, and
the wallet talks to them exactly as it would to the real thing. Going live is a config change.

| Part | What | Docs |
|------|------|------|
| `mock/dict` | DICT API v2.13.0: Pix keys, HTTP + XML | [README](mock/dict/README.md) |
| `mock/spi` | SPI: ISO 20022 payments, returns, reports | [README](mock/spi/README.md) |
| `wallet` | The wallet: Go + gRPC, its own DB, independent of the mocks | [README](wallet/README.md) |

```mermaid
flowchart LR
    User([Wallet user]) -- gRPC --> Wallet
    Wallet -- "key lookup / registration" --> DICT["DICT mock"]
    Wallet -- "pacs.008, pacs.004, trck.002" --> SPI["SPI mock"]
    SPI -- "pacs.002, pacs.008, camt.025" --> Wallet
```

Flows and diagrams: [architecture](docs/architecture.md). Building a participant against the mocks:
[integration guide](docs/integration-guide.md). Plan and decisions: [roadmap](docs/roadmap.md).

## Running

Needs Go 1.27.3+ and Docker.

```sh
make up                # mocks, the wallet's database and Grafana (DICT :8080, SPI :8081, DB :5434, Grafana :3000)
cd wallet
go run ./cmd/api       # gRPC on :9090
go run ./cmd/worker    # in another terminal
make down
```

| Target | |
|--------|-|
| `make sim` | wallet API + worker + simulator on :8090 (after `make up`) |
| `make setup` | only the mocks, with seed data |
| `make check` | closing test against `make setup` |
| `make reset` | mocks back to their seed |
| `make test` | unit tests, no database |
| `make test-integration` | everything on PostgreSQL |
| `make e2e` | mocks + wallet end to end, on throwaway databases (stop `make up` first) |
| `make coverage` | e2e + unit coverage of the wallet |
| `make lint` | gofmt + go vet |
| `make staticcheck`, `make vuln` | staticcheck, govulncheck |
| `make tidy`, `make proto-check` | go.mod and generated gRPC code are up to date |

## Simulator

```sh
make up      # the mocks and Grafana
make sim     # the wallet's API and worker plus the simulator: http://localhost:8090
```

A web page that drives every situation and shows it happening: a wallet customer pays a key (presets for each answer the
SPI's test accounts give: settled, `AC07`, `AC06`, timeout, `FRAD`... and book transfers), a simulated bank pays a wallet
account (`BE01`, `AC14`, blocked, above the limit), returns in both directions, keys registered in the wallet or straight in
the DICT for a bank's account, account status and limits, refused reports, faults (`429`, `503`, slow answers), and the ISO
20022 messages the SPI exchanged. Each action is followed until it is final on both sides, with links to its trace, its
logs and every trace of the payment in Grafana. It is its own module (`simulator/`) and uses the wallet only through its
gRPC API and the mocks only through their HTTP APIs.

The SPI stream carries no trace context, so the SPI's and the wallet worker's traces of one payment are joined by the
`pix.end_to_end_id` attribute every service puts on its spans: `{ span.pix.end_to_end_id = "E..." }` in Tempo finds them all.

## Monitoring

`make up` also starts Grafana at http://localhost:3000 (no login), with Prometheus, Loki and Tempo behind an
OpenTelemetry collector (`grafana/otel-lgtm`). The mocks and the wallet send traces, metrics and logs over OTLP
(`localhost:4318`); the **Pix overview** dashboard (folder `pix_fake`, in [`monitoring/dashboards`](monitoring/dashboards))
shows payments by status and reason, gRPC and HTTP rates and latency, the SPI's outcomes, runtime, and the warnings and
errors with their traces. A trace follows a call from the wallet's API into the DICT or the SPI; logs carry its
`trace_id`. The worker's reading of the SPI stream is not traced (it's a long poll every few seconds).

The mocks export only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set (the compose file does it). The wallet exports to
`localhost:4318` by default; `OTEL_SDK_DISABLED=true` turns it off (`make e2e` does).

## What is simplified

No mTLS, ICP-Brasil certificates or XML signatures: the caller is identified by `X-Participant-ISPB` (DICT) or the
`{ispb}` in the path (SPI). No real settlement or liquidity on Bacen's side. Each mock lists its own simplifications.

## Layout

```
mock/dict, mock/spi     the mocks, each with its own module, DB and README
wallet/                 the wallet; shares nothing with mock/
e2e/                    closing test of the two mocks together
scripts/                e2e.sh, coverage.sh
monitoring/             Grafana dashboards
simulator/              web page to drive and watch every situation (make sim)
docs/                   architecture, integration guide, roadmap
```

## References

- Pix at Bacen: https://www.bcb.gov.br/estabilidadefinanceira/pix (technical docs and the SPI manuals)
- DICT API: https://www.bcb.gov.br/content/estabilidadefinanceira/pix/API-DICT.html
- Bacen Pix API (OpenAPI): https://github.com/bacen/pix-api
- ISO 20022 messages: https://www.iso20022.org/iso-20022-message-definitions

Each mock keeps the spec it was built from in its `docs/`, with version and date. The old `bacen/pix-dict-api` repo is
obsolete.

## License

[Apache License 2.0](LICENSE). See [NOTICE](NOTICE).
