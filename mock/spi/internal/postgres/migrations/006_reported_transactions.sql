-- Transactions a participant settled in its own systems and reported with a trck.002.
CREATE TABLE reported_transactions (
    id                 text        PRIMARY KEY,
    end_to_end_id      text        NOT NULL,
    scenario           text        NOT NULL CHECK (scenario IN ('BOK1', 'BOK2')),
    initiation         text        NOT NULL,
    amount_cents       bigint      NOT NULL CHECK (amount_cents > 0),
    settled_at         timestamptz NOT NULL,
    payer_ispb         text        NOT NULL,
    payer_tax_id       text        NOT NULL,
    payer_branch       text        NOT NULL DEFAULT '',
    payer_account      text        NOT NULL,
    payer_account_type text        NOT NULL,
    payee_ispb         text        NOT NULL,
    payee_tax_id       text        NOT NULL,
    payee_branch       text        NOT NULL DEFAULT '',
    payee_account      text        NOT NULL,
    payee_account_type text        NOT NULL,
    payee_key          text        NOT NULL DEFAULT '',
    reported_by        text        NOT NULL,
    reported_at        timestamptz NOT NULL
);

CREATE INDEX reported_transactions_end_to_end_idx ON reported_transactions (end_to_end_id);
