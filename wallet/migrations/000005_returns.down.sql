DROP INDEX payments_original;
ALTER TABLE payments DROP COLUMN request_id, DROP COLUMN original_payment_id;
