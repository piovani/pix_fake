# SPI mock

Mock of the Central Bank of Brazil (Bacen) **SPI**, the Instant Payment System that settles Pix payments between
participants. **Status: planned** (phase 2 of the [roadmap](../../docs/roadmap.md)). No code yet; this folder holds the
specification the mock will be built from.

## Specification source (decision)

Unlike the DICT, which has a public OpenAPI file, the SPI is specified by **PDF manuals plus a message catalog (XSD)**,
published by Bacen. The mock is built from these documents, and Bacen documentation stays the source of truth.

Downloaded from bcb.gov.br on **2026-11-14** and kept in [`docs/`](docs/):

| File | What it is | Version | Size / pages |
|------|------------|---------|--------------|
| [`Manual-das-Interfaces-de-Comunicacao-1.12.pdf`](docs/Manual-das-Interfaces-de-Comunicacao-1.12.pdf) | Communication interfaces of the SPI (ICOM) and the DICT: how participants send and receive messages | 1.12, published by IN BCB nº 695 of 2025-12-23 | 677 KB, 32 pages |
| [`Manual-de-Fluxos-Efetivacao-do-Pix.pdf`](docs/Manual-de-Fluxos-Efetivacao-do-Pix.pdf) | Pix settlement flows, including returns | Current file at the Bacen URL; version not read | 5.3 MB, 103 pages |
| [`Manual-de-Tempos-do-Pix.pdf`](docs/Manual-de-Tempos-do-Pix.pdf) | Time limits of the Pix process | Current file at the Bacen URL; version not read | 1.1 MB, 35 pages |

SHA-256, to detect when Bacen replaces a file:

```
913d7f0a8274c1e86aa7b3990021a83fb5a20fb003c85414af71b42ecb779fa9  Manual-das-Interfaces-de-Comunicacao-1.12.pdf
0c312291c48ab12d973f5ef62bdd83a6ade75b178daa7852650877740fe7ebca  Manual-de-Fluxos-Efetivacao-do-Pix.pdf
edf73b0a457a18053496426a96673ad64ed6b97ef58e12c0366e097dd70f48b4  Manual-de-Tempos-do-Pix.pdf
```

Sources:

- https://www.bcb.gov.br/content/estabilidadefinanceira/cedsfn/Manual%20das%20Interfaces%20de%20Comunica%C3%A7%C3%A3o-1.12.pdf
- https://www.bcb.gov.br/content/estabilidadefinanceira/pix/Regulamento_Pix/III_ManualdeFluxosdoProcessodeEfetivacaodoPix.pdf
- https://www.bcb.gov.br/content/estabilidadefinanceira/pix/Regulamento_Pix/IX_ManualdeTemposdoPix.pdf

> **Spec reference:** the mock will follow the Manual das Interfaces de Comunicação **v1.12**, as available on the Bacen
> site on **2026-11-14**. If Bacen publishes a newer version after that date, contracts here may differ from the real
> service.

## Still missing

- **Message catalog (Catálogo de Mensagens do SPI, XSD schemas).** Not downloaded yet. It defines the fields of
  `pacs.008`, `pacs.002` and `pacs.004`, and the mock cannot validate messages without it. A Bacen forum presentation of
  2026-03-26 mentions catalog versions 5.12 and 5.13 on its schedule, but the version in force was not confirmed. It
  has to be located on the Bacen site (the manuals page, under technical documentation) and saved in `docs/` with its
  version and date.
- **Manual versions of Fluxos and Tempos.** Only the PDFs were saved; their version numbers still have to be read from
  the documents and recorded above.
- **Content review.** The PDFs were downloaded but not yet read for this project. The delivery model (push or polling),
  authentication and message envelope are decided only after reading the Interfaces manual; until then, this README
  makes no claim about them.

## Next steps

Follow phase 2 of the [roadmap](../../docs/roadmap.md#phase-2-spi-mock-next): finish the specification intake above,
then the delivery model, `pacs.008`, `pacs.002`, `pacs.004` and the report of transactions settled outside the SPI.
