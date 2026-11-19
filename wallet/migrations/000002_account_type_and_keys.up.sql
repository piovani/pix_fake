ALTER TABLE accounts
    ADD COLUMN account_type text NOT NULL DEFAULT 'CACC' CHECK (account_type IN ('CACC', 'SVGS', 'TRAN'));

CREATE TABLE pix_keys (
    key        text        PRIMARY KEY,
    key_type   text        NOT NULL CHECK (key_type IN ('CPF', 'CNPJ', 'PHONE', 'EMAIL', 'EVP')),
    account_id uuid        NOT NULL REFERENCES accounts (id),
    request_id uuid        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX pix_keys_account_id ON pix_keys (account_id);
