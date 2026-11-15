-- Accounts of the participants, used by the simulated participants to decide how to answer a payment.
CREATE TABLE accounts (
    ispb         text NOT NULL,
    number       text NOT NULL,
    branch       text NOT NULL DEFAULT '',
    type         text NOT NULL CHECK (type IN ('CACC', 'SVGS', 'TRAN')),
    holder_tax_id text NOT NULL,
    holder_name  text NOT NULL,
    status       text NOT NULL CHECK (status IN ('ACTIVE', 'CLOSED', 'BLOCKED')),
    scenario     text NOT NULL DEFAULT 'NORMAL' CHECK (scenario IN ('NORMAL', 'NO_ANSWER')),
    PRIMARY KEY (ispb, number)
);
