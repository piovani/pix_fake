-- MED: the transactions settled in the SPI that the DICT knows, and the infraction reports, fraud markers and refunds
-- about them. Amounts are in centavos.
CREATE TABLE transactions (
    id                   text        PRIMARY KEY, -- EndToEndId
    debtor_participant   text        NOT NULL,
    debtor_tax_id        text        NOT NULL,
    creditor_participant text        NOT NULL,
    creditor_tax_id      text        NOT NULL,
    creditor_key         text        NOT NULL DEFAULT '',
    amount_cents         bigint      NOT NULL CHECK (amount_cents > 0),
    settlement_time      timestamptz NOT NULL
);
CREATE INDEX transactions_creditor_tax_id_idx ON transactions (creditor_tax_id);
CREATE INDEX transactions_creditor_key_idx ON transactions (creditor_key) WHERE creditor_key <> '';

CREATE TABLE infraction_reports (
    id               uuid        PRIMARY KEY,
    transaction_id   text        NOT NULL REFERENCES transactions (id),
    reason           text        NOT NULL CHECK (reason IN ('REFUND_REQUEST', 'REFUND_CANCELLED')),
    situation_type   text        NOT NULL,
    report_details   text        NOT NULL DEFAULT '',
    contact_email    text,
    contact_phone    text,
    status           text        NOT NULL CHECK (status IN ('OPEN', 'ACKNOWLEDGED', 'CLOSED', 'CANCELLED')),
    reporter         text        NOT NULL,
    counterparty     text        NOT NULL,
    fraud_marker_id  uuid,
    analysis_result  text        NOT NULL DEFAULT '',
    analysis_details text        NOT NULL DEFAULT '',
    fraud_type       text        NOT NULL DEFAULT '',
    amount_cents     bigint      NOT NULL,
    creditor_tax_id  text        NOT NULL,
    creditor_key     text        NOT NULL DEFAULT '',
    created          timestamptz NOT NULL,
    modified         timestamptz NOT NULL
);
CREATE INDEX infraction_reports_transaction_idx ON infraction_reports (transaction_id);
CREATE INDEX infraction_reports_reporter_idx ON infraction_reports (reporter, modified);
CREATE INDEX infraction_reports_counterparty_idx ON infraction_reports (counterparty, modified);
CREATE INDEX infraction_reports_creditor_tax_id_idx ON infraction_reports (creditor_tax_id);

CREATE TABLE fraud_markers (
    id          uuid        PRIMARY KEY,
    registrant  text        NOT NULL,
    tax_id      text        NOT NULL,
    key         text        NOT NULL DEFAULT '',
    fraud_type  text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('REGISTERED', 'CANCELLED')),
    request_id  uuid,
    report_id   uuid        REFERENCES infraction_reports (id),
    reporter    text        NOT NULL DEFAULT '',
    amount_cents bigint     NOT NULL DEFAULT 0,
    created     timestamptz NOT NULL,
    modified    timestamptz NOT NULL,
    CONSTRAINT fraud_markers_registrant_request_id_key UNIQUE (registrant, request_id)
);
CREATE INDEX fraud_markers_tax_id_idx ON fraud_markers (tax_id, modified);
CREATE INDEX fraud_markers_key_idx ON fraud_markers (key) WHERE key <> '';

CREATE TABLE refunds (
    id                    uuid        PRIMARY KEY,
    transaction_id        text        NOT NULL REFERENCES transactions (id),
    reason                text        NOT NULL,
    amount_cents          bigint      NOT NULL CHECK (amount_cents > 0),
    details               text        NOT NULL DEFAULT '',
    status                text        NOT NULL CHECK (status IN ('OPEN', 'CLOSED', 'CANCELLED')),
    requesting            text        NOT NULL,
    contested             text        NOT NULL,
    infraction_report_id  uuid        REFERENCES infraction_reports (id),
    analysis_result       text        NOT NULL DEFAULT '',
    analysis_details      text        NOT NULL DEFAULT '',
    rejection_reason      text        NOT NULL DEFAULT '',
    refund_transaction_id text        NOT NULL DEFAULT '',
    created               timestamptz NOT NULL,
    modified              timestamptz NOT NULL
);
CREATE INDEX refunds_transaction_idx ON refunds (transaction_id);
CREATE INDEX refunds_requesting_idx ON refunds (requesting, modified);
CREATE INDEX refunds_contested_idx ON refunds (contested, modified);
