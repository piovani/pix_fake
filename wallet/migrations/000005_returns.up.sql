ALTER TABLE payments
    ADD COLUMN original_payment_id uuid REFERENCES payments (id),
    ADD COLUMN request_id          uuid UNIQUE;

CREATE INDEX payments_original ON payments (original_payment_id) WHERE original_payment_id IS NOT NULL;
