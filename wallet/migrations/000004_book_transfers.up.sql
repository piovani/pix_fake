ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('CREDIT', 'RESERVE', 'SETTLE', 'RELEASE', 'DEBIT'));

ALTER TABLE resolutions
    ADD COLUMN book_transfer     boolean NOT NULL DEFAULT false,
    ADD COLUMN payee_account_id  uuid REFERENCES accounts (id);

ALTER TABLE payments DROP CONSTRAINT payments_end_to_end_id_key;
ALTER TABLE payments ADD CONSTRAINT payments_end_to_end_id_direction_key UNIQUE (end_to_end_id, direction);

ALTER TABLE payments
    ADD COLUMN report_status  text        NOT NULL DEFAULT '' CHECK (report_status IN ('', 'SENT', 'ACCEPTED', 'REJECTED')),
    ADD COLUMN report_sent_at timestamptz,
    ADD COLUMN report_reason  text        NOT NULL DEFAULT '';

CREATE INDEX payments_unreported ON payments (finished_at)
    WHERE type = 'BOOK_TRANSFER' AND direction = 'OUTBOUND' AND status = 'SETTLED' AND report_status IN ('', 'SENT');
CREATE INDEX payments_pending_book ON payments (created_at) WHERE status = 'PENDING' AND type = 'BOOK_TRANSFER';
