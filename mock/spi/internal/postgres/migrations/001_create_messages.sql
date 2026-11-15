CREATE TABLE messages (
    seq         bigserial   PRIMARY KEY,
    resource_id text        NOT NULL UNIQUE,
    direction   text        NOT NULL CHECK (direction IN ('in', 'out')),
    ispb        text        NOT NULL,
    body        bytea       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX messages_out_idx ON messages (ispb, seq) WHERE direction = 'out';

-- A read stream: where a participant is in its output queue. Each read that returns
-- messages opens a new one (the PI-Pull-Next token); the old ones keep working, so a
-- retried read gets the same messages again.
CREATE TABLE streams (
    token      text        PRIMARY KEY,
    ispb       text        NOT NULL,
    cursor     bigint      NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Last message each participant acknowledged (DELETE of a stream).
CREATE TABLE cursors (
    ispb  text   PRIMARY KEY,
    acked bigint NOT NULL
);
