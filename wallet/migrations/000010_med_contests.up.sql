-- MED, the wallet as the payer's participant: a customer contests a payment, the wallet reports it in the DICT and asks
-- for the refund when the participant that received it agrees.
CREATE TABLE med_contests (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid        NOT NULL,
    payment_id     uuid        NOT NULL,
    transaction_id text        NOT NULL, -- EndToEndId of the payment
    request_id     uuid        NOT NULL UNIQUE,
    situation      text        NOT NULL,
    details        text        NOT NULL DEFAULT '',
    amount_cents   bigint      NOT NULL CHECK (amount_cents > 0),
    -- OPENING until the report is in the DICT; AGREED until the refund is
    status         text        NOT NULL CHECK (status IN ('OPENING', 'REPORTED', 'AGREED', 'REFUND_REQUESTED', 'REFUNDED', 'REJECTED', 'CANCELLED')),
    report_id      text        NOT NULL DEFAULT '',
    refund_id      text        NOT NULL DEFAULT '',
    refunded_cents bigint      NOT NULL DEFAULT 0,
    return_id      text        NOT NULL DEFAULT '', -- RtrId of the return that brought the money back
    rejection      text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- One contest in progress per payment.
CREATE UNIQUE INDEX med_contests_in_progress ON med_contests (payment_id)
    WHERE status IN ('OPENING', 'REPORTED', 'AGREED', 'REFUND_REQUESTED');
CREATE INDEX med_contests_account ON med_contests (account_id, created_at DESC);
CREATE INDEX med_contests_report ON med_contests (report_id) WHERE report_id <> '';
CREATE INDEX med_contests_refund ON med_contests (refund_id) WHERE refund_id <> '';
