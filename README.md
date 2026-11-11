# pix_fake

A digital wallet with Pix integration, built against a **mocked** version of the Central Bank of Brazil (Bacen) Pix infrastructure.

The real Pix ecosystem is only reachable by regulated participants. To develop and test a wallet without that access, this project first builds mocks that simulate the Bacen systems, using the **same message formats and contracts Bacen documents**. The wallet talks to the mocks exactly as it would talk to the real thing, so moving to the real infrastructure later is a configuration change rather than a rewrite.

## Scope

| Phase | Component | Status |
|-------|-----------|--------|
| 1 | **DICT mock**: Pix key directory | Planned |
| 2 | **SPI mock**: instant payment settlement | Planned |
| 3 | Digital wallet + Pix integration layer | Planned |

Mocks are built in this order, DICT first, then SPI. At this stage the repository only contains documentation.

### What the mocks do **not** do

- No mutual TLS or ICP-Brasil certificate authentication between participants. Requests are accepted as-is.
- No message signing (XML digital signatures on SPI messages).
- No real settlement, reserve accounts or liquidity management on Bacen's side.

Contracts, field names and flows follow the Bacen documentation. Security is intentionally simplified.

## Architecture

```mermaid
flowchart LR
    User([Wallet user]) --> Wallet

    subgraph Ours["Our system"]
        Wallet["Digital Wallet"]
        PixInt["Pix integration layer"]
        Wallet --> PixInt
    end

    subgraph Mocks["Mocked Bacen infrastructure"]
        DICT["DICT mock<br/>Pix key directory"]
        SPI["SPI mock<br/>Settlement"]
    end

    PixInt -- "key lookup / registration" --> DICT
    PixInt -- "payments (ISO 20022)" --> SPI
    SPI -- "status / incoming payments" --> PixInt
```

More diagrams (component view, outbound and inbound transfer flows) are in [docs/architecture.md](docs/architecture.md).

## Mocked systems

### 1. DICT: Directory of Transactional Account Identifiers

The DICT stores Pix keys and links each key to its owner and transactional account. Participants query it before a payment to resolve who is behind a key.

Functionality to mock:

| Operation | Description |
|-----------|-------------|
| Create entry | Register a Pix key for an account |
| Get entry | Look up a key and return owner and account data |
| Update entry | Change the account data linked to a key |
| Delete entry | Remove a key |
| Check keys | Verify whether keys exist |
| Claims / portability | Move a key between participants (later phase) |

Key types: `CPF`, `CNPJ`, `EMAIL`, `PHONE`, `EVP` (random key).

**Transport:** HTTPS REST. Bacen's DICT API exchanges **XML**.

**Contract sketch (Get entry):** the exact schema will be taken from the Bacen specification and stored under `docs/contracts/`.

```
GET /entries/{key}
```

```xml
<GetEntryResponse>
  <Key>alice@example.com</Key>
  <KeyType>EMAIL</KeyType>
  <Account>
    <Participant>12345678</Participant>   <!-- ISPB, 8 digits -->
    <Branch>0001</Branch>
    <AccountNumber>1234567</AccountNumber>
    <AccountType>CACC</AccountType>       <!-- CACC | SVGS | SLRY | TRAN -->
    <OpeningDate>2024-01-15T10:00:00Z</OpeningDate>
  </Account>
  <Owner>
    <Type>NATURAL_PERSON</Type>           <!-- NATURAL_PERSON | LEGAL_PERSON -->
    <TaxIdNumber>12345678901</TaxIdNumber>
    <Name>Alice Silva</Name>
  </Owner>
  <CreationDate>2024-01-15T10:00:00Z</CreationDate>
</GetEntryResponse>
```

### 2. SPI: Instant Payment System

The SPI settles Pix payments in real time between participants. Payments are exchanged as **ISO 20022** XML messages.

Messages to mock:

| Message | Direction | Purpose |
|---------|-----------|---------|
| `pacs.008` | Participant → SPI, SPI → Participant | Credit transfer (the payment order) |
| `pacs.002` | SPI → Participant, Participant → SPI | Payment status report (settled / rejected) |
| `pacs.004` | Participant → SPI | Return of a settled payment (refund) |
| `camt.*` | Both | Cancellation and status queries (later phase) |

**Contract sketch (`pacs.008`, simplified):**

```xml
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08">
  <FIToFICstmrCdtTrf>
    <GrpHdr>
      <MsgId>M12345678202411061200abcdef12345</MsgId>
      <CreDtTm>2024-11-06T12:00:00Z</CreDtTm>
      <NbOfTxs>1</NbOfTxs>
    </GrpHdr>
    <CdtTrfTxInf>
      <PmtId>
        <EndToEndId>E1234567820241106120000000000001</EndToEndId>
      </PmtId>
      <IntrBkSttlmAmt Ccy="BRL">10.00</IntrBkSttlmAmt>
      <Dbtr><Nm>Bob Souza</Nm></Dbtr>
      <DbtrAcct>...</DbtrAcct>
      <DbtrAgt>...</DbtrAgt>      <!-- payer ISPB -->
      <CdtrAgt>...</CdtrAgt>      <!-- receiver ISPB -->
      <Cdtr><Nm>Alice Silva</Nm></Cdtr>
      <CdtrAcct>...</CdtrAcct>
      <RmtInf><Ustrd>Lunch</Ustrd></RmtInf>
    </CdtTrfTxInf>
  </FIToFICstmrCdtTrf>
</Document>
```

`EndToEndId` is the unique payment identifier: `E` + payer ISPB (8) + date/time `yyyyMMddHHmm` (12) + 11 alphanumeric characters = 32 characters.

The `pacs.002` response carries the status of the transaction (for example `ACSC` for settled and `RJCT` for rejected, with a reason code).

## Official references

Bacen documentation is the source of truth for every contract in this project. Links are listed so the mocks can be checked against the originals:

- Pix, Bacen home: https://www.bcb.gov.br/estabilidadefinanceira/pix
- Pix technical specifications and manuals (DICT, SPI, message catalog): https://www.bcb.gov.br/estabilidadefinanceira/pix, under "Documentação técnica"
- Bacen Pix API (OpenAPI specs, GitHub): https://github.com/bacen/pix-api
- ISO 20022 message catalog: https://www.iso20022.org/iso-20022-message-definitions

> **Note:** verify the links against the Bacen site, since it reorganizes its documentation pages from time to time. When a contract is implemented, its source document and version should be recorded in `docs/contracts/`.

## Planned layout

```
pix_fake/
├── README.md
└── docs/
    ├── architecture.md      # diagrams and flows
    └── contracts/           # DICT and SPI message contracts (to be added)
```

## License

To be defined.
