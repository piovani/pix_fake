CREATE TABLE entries (
    key                text        PRIMARY KEY,
    key_type           text        NOT NULL CHECK (key_type IN ('CPF', 'CNPJ', 'PHONE', 'EMAIL', 'EVP')),
    participant        text        NOT NULL,
    branch             text        NOT NULL DEFAULT '',
    account_number     text        NOT NULL,
    account_type       text        NOT NULL,
    opening_date       timestamptz NOT NULL,
    owner_type         text        NOT NULL CHECK (owner_type IN ('NATURAL_PERSON', 'LEGAL_PERSON')),
    owner_tax_id       text        NOT NULL,
    owner_name         text        NOT NULL,
    owner_trade_name   text        NOT NULL DEFAULT '',
    request_id         uuid        NOT NULL,
    creation_date      timestamptz NOT NULL,
    key_ownership_date timestamptz NOT NULL,
    CONSTRAINT entries_participant_request_id_key UNIQUE (participant, request_id)
);
