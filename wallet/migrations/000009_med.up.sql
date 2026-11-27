-- MED, the wallet as the payee's participant. A block holds money like a reservation, but no payment owns it, so the
-- sweep of orphan reservations leaves it alone. The med tables point at payments, accounts and reservations by id
-- only, with no foreign keys: each module keeps to its own tables.
ALTER TABLE reservations ADD COLUMN purpose text NOT NULL DEFAULT 'PAYMENT' CHECK (purpose IN ('PAYMENT', 'BLOCK'));

ALTER TABLE payments ADD COLUMN return_reason text NOT NULL DEFAULT '';
UPDATE payments SET return_reason = 'MD06' WHERE type = 'RETURN' AND direction = 'OUTBOUND';

-- The infraction reports of the DICT against payments the wallet received.
CREATE TABLE med_reports (
    id             text        PRIMARY KEY, -- the DICT's InfractionReportId
    transaction_id text        NOT NULL,    -- EndToEndId of the payment reported
    payment_id     uuid,
    account_id     uuid,
    status         text        NOT NULL CHECK (status IN ('OPEN', 'ACKNOWLEDGED', 'CLOSED', 'CANCELLED')),
    situation      text        NOT NULL,
    details        text        NOT NULL DEFAULT '',
    amount_cents   bigint      NOT NULL,    -- InfractionAmount: what the DICT asks to block
    block_id       uuid,        -- a reservation of the ledger
    blocked_cents  bigint      NOT NULL DEFAULT 0,
    -- HELD while the money is blocked; RELEASED when given back, RETURNED when it went into the return of a refund
    block_status   text        NOT NULL DEFAULT '' CHECK (block_status IN ('', 'HELD', 'RELEASED', 'RETURNED')),
    result         text        NOT NULL DEFAULT '' CHECK (result IN ('', 'AGREED', 'DISAGREED')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX med_reports_transaction ON med_reports (transaction_id);

-- The refunds the DICT asks of the wallet, and the return that answers each.
CREATE TABLE med_refunds (
    id                text        PRIMARY KEY, -- the DICT's RefundId
    transaction_id    text        NOT NULL,
    payment_id        uuid,
    reason            text        NOT NULL,
    amount_cents      bigint      NOT NULL,
    status            text        NOT NULL CHECK (status IN ('RETURNING', 'CLOSED')),
    request_id        uuid        NOT NULL UNIQUE, -- of the return, so a retry makes the same one
    return_cents      bigint      NOT NULL DEFAULT 0,
    return_payment_id uuid,
    result            text        NOT NULL DEFAULT '',
    rejection_reason  text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX med_refunds_returning ON med_refunds (created_at) WHERE status = 'RETURNING';

-- How far the wallet has read the changes of the DICT, per list.
CREATE TABLE med_watermarks (
    name text        PRIMARY KEY,
    at   timestamptz NOT NULL
);
