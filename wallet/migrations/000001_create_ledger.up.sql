CREATE TABLE accounts (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    branch          text        NOT NULL,
    number          text        NOT NULL,
    holder_name     text        NOT NULL,
    tax_id          text        NOT NULL,
    available_cents bigint      NOT NULL DEFAULT 0 CHECK (available_cents >= 0),
    reserved_cents  bigint      NOT NULL DEFAULT 0 CHECK (reserved_cents >= 0),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (branch, number)
);

CREATE TABLE reservations (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   uuid        NOT NULL REFERENCES accounts (id),
    amount_cents bigint      NOT NULL CHECK (amount_cents > 0),
    reference    text        NOT NULL UNIQUE,
    status       text        NOT NULL CHECK (status IN ('HELD', 'SETTLED', 'RELEASED')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id   uuid        NOT NULL REFERENCES accounts (id),
    kind         text        NOT NULL CHECK (kind IN ('CREDIT', 'RESERVE', 'SETTLE', 'RELEASE')),
    amount_cents bigint      NOT NULL CHECK (amount_cents > 0),
    reference    text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, kind, reference)
);
