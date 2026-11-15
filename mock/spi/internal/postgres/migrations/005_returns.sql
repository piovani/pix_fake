-- A return (pacs.004) goes through the same cycle as a payment, so it lives in the same table. A payment is
-- identified by its EndToEndId, a return by its RtrId; a return also carries the EndToEndId of the payment it returns.
ALTER TABLE payments RENAME COLUMN end_to_end_id TO id;
ALTER TABLE payments ADD COLUMN end_to_end_id text;
UPDATE payments SET end_to_end_id = id;
ALTER TABLE payments ALTER COLUMN end_to_end_id SET NOT NULL;
ALTER TABLE payments ADD COLUMN kind text NOT NULL DEFAULT 'PAYMENT' CHECK (kind IN ('PAYMENT', 'RETURN'));
ALTER TABLE payments ADD COLUMN return_reason text NOT NULL DEFAULT '';
CREATE INDEX payments_end_to_end_idx ON payments (end_to_end_id);
