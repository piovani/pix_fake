-- A resolution is the lookup of a key before a payment: who will be paid, and the EndToEndId the payment will carry.
CREATE TABLE resolutions (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id    uuid        NOT NULL REFERENCES accounts (id),
    key           text        NOT NULL,
    end_to_end_id text        NOT NULL UNIQUE,
    payee         jsonb       NOT NULL,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE payments (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid        NOT NULL REFERENCES accounts (id),
    direction      text        NOT NULL CHECK (direction IN ('OUTBOUND', 'INBOUND')),
    type           text        NOT NULL CHECK (type IN ('PIX', 'BOOK_TRANSFER', 'RETURN')),
    status         text        NOT NULL CHECK (status IN ('PENDING', 'SETTLED', 'REJECTED')),
    amount_cents   bigint      NOT NULL CHECK (amount_cents > 0),
    end_to_end_id  text        NOT NULL UNIQUE,
    -- One resolution makes at most one payment. Null for what did not start from a resolution (a received payment).
    resolution_id  uuid        UNIQUE REFERENCES resolutions (id),
    -- The money held in the ledger while the payment is in flight.
    reservation_id uuid,
    counterparty   jsonb       NOT NULL,
    message        text        NOT NULL DEFAULT '',
    reject_code    text        NOT NULL DEFAULT '',
    reject_reason  text        NOT NULL DEFAULT '',
    -- The PI-ResourceId the SPI gave the pacs.008; null until the SPI accepted it. An admi.002 names it.
    resource_id    text,
    submitted_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz
);

CREATE INDEX payments_account_created ON payments (account_id, created_at DESC, id DESC);
CREATE INDEX payments_resource_id ON payments (resource_id) WHERE resource_id IS NOT NULL;
-- What the worker looks for: payments the SPI has not accepted yet.
CREATE INDEX payments_unsubmitted ON payments (created_at) WHERE status = 'PENDING' AND submitted_at IS NULL;
