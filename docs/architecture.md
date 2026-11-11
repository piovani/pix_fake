# Architecture

This document describes how the wallet interacts with the **mocked** Central Bank (Bacen) Pix infrastructure.

## Component view

```mermaid
flowchart LR
    User([Wallet user]) --> Wallet

    subgraph Ours["Our system"]
        Wallet["Digital Wallet<br/>(accounts, balances, ledger)"]
        PixInt["Pix integration layer<br/>(PSP / participant side)"]
        Wallet --> PixInt
    end

    subgraph Mocks["Mocked Bacen infrastructure (this repo)"]
        DICT["DICT mock<br/>Pix key directory<br/>REST"]
        SPI["SPI mock<br/>Instant payment settlement<br/>ISO 20022"]
    end

    PixInt -- "lookup / register / delete keys" --> DICT
    PixInt -- "pacs.008 (send payment)" --> SPI
    SPI -- "pacs.002 (status report)" --> PixInt
    SPI -. "pacs.008 (incoming payment)" .-> PixInt
```

Replacing the mocks with the real DICT and SPI is the long-term goal: the Pix integration layer should only depend on the Bacen contracts, never on mock internals.

## Outbound transfer (wallet user pays a Pix key)

```mermaid
sequenceDiagram
    autonumber
    actor U as Wallet user
    participant W as Wallet
    participant P as Pix integration layer
    participant D as DICT mock
    participant S as SPI mock

    U->>W: Pay key "alice@example.com", R$ 10.00
    W->>P: Initiate payment
    P->>D: GET /entries/{key}
    D-->>P: Key owner + account (ISPB, branch, account)
    P->>W: Confirm recipient data
    W->>W: Reserve balance
    P->>S: pacs.008 (credit transfer, EndToEndId)
    S-->>P: pacs.002 (ACSC = settled / RJCT = rejected)
    P->>W: Settle or release reservation
    W-->>U: Receipt
```

## Inbound transfer (someone pays a wallet user)

```mermaid
sequenceDiagram
    autonumber
    participant S as SPI mock
    participant P as Pix integration layer
    participant W as Wallet

    S->>P: pacs.008 (credit transfer to our participant)
    P->>W: Validate recipient account
    W->>W: Credit balance
    P-->>S: pacs.002 (ACSC / RJCT)
```
