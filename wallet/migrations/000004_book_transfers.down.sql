DROP INDEX payments_pending_book;
DROP INDEX payments_unreported;
ALTER TABLE payments DROP COLUMN report_reason, DROP COLUMN report_sent_at, DROP COLUMN report_status;
ALTER TABLE payments DROP CONSTRAINT payments_end_to_end_id_direction_key;
ALTER TABLE payments ADD CONSTRAINT payments_end_to_end_id_key UNIQUE (end_to_end_id);
ALTER TABLE resolutions DROP COLUMN payee_account_id, DROP COLUMN book_transfer;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check CHECK (kind IN ('CREDIT', 'RESERVE', 'SETTLE', 'RELEASE'));
