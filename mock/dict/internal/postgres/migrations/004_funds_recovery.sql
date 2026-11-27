-- MED: funds recoveries, the infraction reports and refunds they create, and the event notifications about them.
CREATE TABLE funds_recoveries (
    id                  uuid        PRIMARY KEY,
    root_transaction_id text        NOT NULL UNIQUE REFERENCES transactions (id), -- one per root transaction, ever
    situation_type      text        NOT NULL,
    report_details      text        NOT NULL DEFAULT '',
    contact_email       text        NOT NULL,
    contact_phone       text        NOT NULL,
    status              text        NOT NULL CHECK (status IN ('CREATED', 'TRACKED', 'AWAITING_ANALYSIS', 'ANALYSED',
                                                               'REFUNDING', 'COMPLETED', 'CANCELLED')),
    reporter            text        NOT NULL,
    graph               jsonb       NOT NULL, -- the tracking graph, drawn once when it is created
    created             timestamptz NOT NULL,
    modified            timestamptz NOT NULL
);

ALTER TABLE infraction_reports
    ADD COLUMN funds_recovery_id uuid REFERENCES funds_recoveries (id),
    ADD COLUMN transaction_depth integer NOT NULL DEFAULT 0;
CREATE INDEX infraction_reports_funds_recovery_idx ON infraction_reports (funds_recovery_id) WHERE funds_recovery_id IS NOT NULL;

ALTER TABLE refunds ADD COLUMN funds_recovery_id uuid REFERENCES funds_recoveries (id);
CREATE INDEX refunds_funds_recovery_idx ON refunds (funds_recovery_id) WHERE funds_recovery_id IS NOT NULL;

-- the tracking follows the money out of an account: the transactions a user paid at a participant
CREATE INDEX transactions_debtor_idx ON transactions (debtor_participant, debtor_tax_id, settlement_time);

CREATE TABLE event_notifications (
    id          bigserial   PRIMARY KEY,
    event       text        NOT NULL,
    entity_id   uuid        NOT NULL,
    participant text        NOT NULL,
    at          timestamptz NOT NULL
);
CREATE INDEX event_notifications_participant_idx ON event_notifications (participant, id);
