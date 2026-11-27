-- The DICT is told of every payment that settles (MED). The settled payments it has not been told of yet are the
-- outbox: settling a payment and queueing it for the DICT are the same update, so none is lost.
ALTER TABLE payments ADD COLUMN dict_notified_at timestamptz;
CREATE INDEX payments_dict_outbox_idx ON payments (settled_at, id)
    WHERE kind = 'PAYMENT' AND status = 'SETTLED' AND dict_notified_at IS NULL;
