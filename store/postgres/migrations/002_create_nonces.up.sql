CREATE TABLE IF NOT EXISTS nonces (
    nonce      TEXT PRIMARY KEY,
    consumed   BOOLEAN NOT NULL DEFAULT FALSE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_nonces_expires_at ON nonces (expires_at);
