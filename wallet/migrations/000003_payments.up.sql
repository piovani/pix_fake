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
    resolution_id  uuid        UNIQUE REFERENCES resolutions (id),
    reservation_id uuid,
    counterparty   jsonb       NOT NULL,
    message        text        NOT NULL DEFAULT '',
    reject_code    text        NOT NULL DEFAULT '',
    reject_reason  text        NOT NULL DEFAULT '',
    resource_id    text,
    submitted_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz
);

CREATE INDEX payments_account_created ON payments (account_id, created_at DESC, id DESC);
CREATE INDEX payments_resource_id ON payments (resource_id) WHERE resource_id IS NOT NULL;
CREATE INDEX payments_unsubmitted ON payments (created_at) WHERE status = 'PENDING' AND submitted_at IS NULL;
