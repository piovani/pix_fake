# SPI message catalog

Message specification for the SPI mock: the ISO 20022 message schemas (XSD), the field-filling guides (XLSX), sample
messages and the catalog document that describes them. Downloaded from bcb.gov.br on **2026-11-14**.

## Version in force

| Item | Value |
|------|-------|
| Catálogo de Serviços do SFN, Volume VI (Pagamentos Instantâneos) | **5.13**, published 2026-07-24 |
| Message package | **`spi.5.13.2.zip`**, published 2026-10-02 (latest found; `5.13.3`, `5.13.4` and `5.14.1` do not exist at the Bacen URL) |
| Package contents | `xsd/`, `xlsx/`, `exemplos/`, version notes and a diff against 5.13.1 |

The version in an XSD file name (`pacs.008.spi.1.16`) is the version of **that message**, not of the catalog.

## Contents

| Path | What it is |
|------|------------|
| [`Catalogo_de_Servicos_do_SFN_Volume_VI_Versao_513.pdf`](Catalogo_de_Servicos_do_SFN_Volume_VI_Versao_513.pdf) | The catalog document (42 pages): message cycles, flow diagrams, revision history |
| [`spi.5.13.2.zip`](spi.5.13.2.zip) | Original package, as published |
| [`spi.5.13.2/xsd/`](spi.5.13.2/xsd/) | XML schemas, one per message (28 files) |
| [`spi.5.13.2/xlsx/`](spi.5.13.2/xlsx/) | Filling guides: every field, whether it is mandatory, its rules |
| [`spi.5.13.2/exemplos/`](spi.5.13.2/exemplos/) | Sample XML messages per type (valid and with error) |
| [`spi.5.13.2/NotasDeVersao.pdf`](spi.5.13.2/NotasDeVersao.pdf) | Release notes of the package |
| [`reference/`](reference/) | Related manuals used to fill gaps (see below) |

SHA-256:

```
0780ff8befd73e70c40e798dc16e90f6a296dd132d59f2d4d3e57ca4c5e3f541  Catalogo_de_Servicos_do_SFN_Volume_VI_Versao_513.pdf
8ac114c30ee6d15535387047aff68bf6fdf8f3f7f8bfd2a6ab0e3d33aa93ecbe  spi.5.13.2.zip
fd4422bac3bb3237d26decedc8725c08449aeb7729c243165fbe5832cb236b3d  reference/Manual-de-Padroes-para-Iniciacao-do-Pix.pdf
68be96829aea4fa7afa16e1c24ae2ab6d44e4a09a15fb14611b555cb72730f89  reference/Guia-Implementacao-Canal-Secundario.pdf
```

Sources:

- https://www.bcb.gov.br/content/estabilidadefinanceira/cedsfn/Catalogos/Catalogo_de_Servicos_do_SFN_Volume_VI_Versao_513.pdf
- https://www.bcb.gov.br/content/estabilidadefinanceira/cedsfn/Catalogos/spi.5.13.2.zip
- https://www.bcb.gov.br/content/estabilidadefinanceira/pix/Regulamento_Pix/II_ManualdePadroesparaIniciacaodoPix.pdf
  (version 2.10.0; the page also links a 2.11.0-rc1 release candidate, not used)
- https://www.bcb.gov.br/content/estabilidadefinanceira/pix/Guia_Implementacao_Canal_Secundario_Transmissao_Mensagens.pdf

The `zip` URL was found by following the catalog's own reference (it points to the `comunicacaodados` page, which is
rendered by JavaScript and cannot be read without a browser) and probing the neighbouring file names.

**Checked on the page itself on 2026-11-14:** it lists the SPI message definitions `5.12.1` (2026-03-27) and `5.13.2`
(2026-10-02), the latter being the newest, so the package here is current. It also lists Volume VI `5.13`, the Manual das
Interfaces de Comunicação `1.12` (2025-12-23) and the Manual de Segurança do SFN Volume II `6.01` (2026-11-09; not
collected, security is out of scope). Catalog 5.13 went to production for the SPI on 2026-10-25. The page also offers a
Dicionário de Domínios (2026-10-09) and a Relação de Códigos de Erros (2026-09-04), not collected: the domain tables
inside the XLSX files cover what the mock uses.

## Messages in the package

Schemas in `xsd/` (message version in the file name):

| Group | Messages |
|-------|----------|
| Payment | `pacs.008` 1.16 (credit transfer), `pacs.002` 1.18 (status report), `pacs.004` 1.5 (return) |
| Reporting outside the SPI | `trck.002` 1.1 |
| Errors and control | `admi.002` 1.5, `admi.004` 1.2 |
| Account and statements | `camt.052` 1.3, `camt.053` 1.4, `camt.054` 1.16, `camt.060` 1.9, `camt.014` 1.6, `camt.025` 1.0 |
| Cancellation | `camt.029` 1.2, `camt.055` 1.1 |
| Participants and reference data | `reda.014`, `reda.016`, `reda.017`, `reda.022`, `reda.031`, `reda.041`, `pibr.001`, `pibr.002` |
| Pix Automático | `pain.009`, `pain.011`, `pain.012`, `pain.013`, `pain.014` |
| Signature | `xmldsig.spi.1.0` (the mock does not validate signatures) |

Phase 2 of the roadmap needs `pacs.008`, `pacs.002`, `pacs.004`, `trck.002` and `admi.002`.

## Findings that shape the mock

Read from the XSDs and sample messages.

- **`EndToEndId`** (`pacs.008`): 32 characters, pattern
  `E` + ISPB (8 chars) + date and time `yyyyMMddHHmm` (12 digits) + 11 alphanumeric characters. This closes the
  roadmap follow-up on the DICT's `PI-EndToEndId` format.
- **ISPB** pattern: 8 characters, `[0-9A-Z]{8}`.
- **Message envelope**: each message is an `Envelope` with an `AppHdr` (`Fr` and `To` ISPBs, `BizMsgIdr`, `MsgDefIdr`,
  `CreDt`, `Sgntr`) and a `Document`. The namespace carries the message version, for example
  `https://www.bcb.gov.br/pi/pacs.008/1.16`. The sample ISPBs of the examples are fictional.
- **Channel** is chosen by `SvcLvl/Prtry` in `pacs.008`: `PAGPRI` and `PAGFRD` use the primary channel, `PAGAGD` the
  secondary one. Later messages of the same payment must use the same channel; the wrong channel produces an
  `admi.002`.
- **Idempotency key** of `pacs.008` is the `EndToEndId`.
- **Status and reason codes** of `pacs.002`: `ACSP` (receiving participant accepts), `ACSC` (SPI tells the payer's
  participant the payment is settled), `ACCC` (SPI tells the receiver's participant) and `RJCT` (rejected, with a reason).
  The meaning of every code, and who generates it, is in the `Tabela de Domínios` sheet of `PACS002.xlsx`; the data
  contract summarizes it.
- **Discrepancy in the Interfaces manual v1.12.** Its catalog example lists `pacs.008.spi.1.4` on input and `1.3` on
  output, and `pacs.002.spi.1.3`. The package has `pacs.008` 1.16 and `pacs.002` 1.18, so that example is out of date.
  The mock should read versions from the package, not from the manual.

## `reference/`

| File | Why it is here |
|------|----------------|
| `Manual-de-Padroes-para-Iniciacao-do-Pix.pdf` | Version 2.10.0, 123 pages. Source for who generates the `EndToEndId` (the payment initiator, mandatory since 2022-09-25) and for the initiation rules |
| `Guia-Implementacao-Canal-Secundario.pdf` | 13 pages. Rules of the secondary channel (scheduled payments), complementing the Interfaces manual |

## Not collected

- **Manual de Segurança do SFN**, volume II: signatures and certificates, out of scope (security is simplified on
  purpose, see the roadmap principles).
- **Manual Operacional do DICT**: DICT only.
- **Pix Automático (`pain.*`) rules**: schemas are kept, flows are not in scope of phase 2.
