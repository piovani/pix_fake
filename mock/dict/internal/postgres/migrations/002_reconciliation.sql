-- Reconciliation (CIDs). The service fills cid; the entries from before this migration get theirs when the mock
-- starts (see backfillCIDs), since the CID is an HMAC computed in Go.
ALTER TABLE entries ADD COLUMN cid text;

-- The seed no longer gives the wallet's Padaria a trade name, since the wallet registers keys without one: a database
-- seeded before follows, so the CIDs of both sides agree.
UPDATE entries SET owner_trade_name = ''
WHERE participant = '99000001' AND key IN ('11222333000181', 'contato@paoquente.example.com') AND owner_trade_name = 'Padaria Pão Quente';
CREATE UNIQUE INDEX entries_cid_key ON entries (cid);
CREATE INDEX entries_participant_key_type_idx ON entries (participant, key_type);

-- Every change to the CIDs of a participant and key type, written with the change itself. The VSync at any moment is
-- the XOR of the events until then.
CREATE TABLE cid_events (
    seq         bigserial   PRIMARY KEY,
    participant text        NOT NULL,
    key_type    text        NOT NULL,
    type        text        NOT NULL CHECK (type IN ('ADDED', 'REMOVED')),
    cid         text        NOT NULL,
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cid_events_scope_idx ON cid_events (participant, key_type, seq);

CREATE TABLE sync_verifications (
    id                   bigserial   PRIMARY KEY,
    participant          text        NOT NULL,
    key_type             text        NOT NULL,
    participant_verifier text        NOT NULL,
    dict_verifier        text        NOT NULL,
    result               text        NOT NULL CHECK (result IN ('OK', 'NOK')),
    created_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE cid_files (
    id            bigserial   PRIMARY KEY,
    participant   text        NOT NULL,
    key_type      text        NOT NULL,
    status        text        NOT NULL,
    request_time  timestamptz NOT NULL,
    creation_time timestamptz,
    content       bytea       NOT NULL,
    sha256        text        NOT NULL
);
