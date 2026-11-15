CREATE TABLE payments (
    end_to_end_id      text        PRIMARY KEY,
    status             text        NOT NULL CHECK (status IN ('PENDING', 'SETTLED', 'REJECTED')),
    reason_code        text        NOT NULL DEFAULT '',
    reason_detail      text        NOT NULL DEFAULT '',
    amount_cents       bigint      NOT NULL CHECK (amount_cents > 0),
    purpose            text        NOT NULL,
    initiation         text        NOT NULL,
    info               text        NOT NULL DEFAULT '',
    priority           text        NOT NULL,
    payer_ispb         text        NOT NULL,
    payer_name         text        NOT NULL,
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
    accepted_at        timestamptz NOT NULL,
    created_at         timestamptz NOT NULL,
    settled_at         timestamptz
);

CREATE INDEX payments_payer_idx ON payments (payer_ispb, created_at);
CREATE INDEX payments_payee_idx ON payments (payee_ispb, created_at);
