CREATE TABLE IF NOT EXISTS transfers (
    id                TEXT PRIMARY KEY,
    kind              TEXT NOT NULL,
    mode              TEXT NOT NULL,
    status            TEXT NOT NULL,
    asset_code        TEXT NOT NULL DEFAULT '',
    asset_issuer      TEXT NOT NULL DEFAULT '',
    account           TEXT NOT NULL,
    amount            TEXT NOT NULL DEFAULT '',
    interactive_token TEXT NOT NULL DEFAULT '',
    interactive_url   TEXT NOT NULL DEFAULT '',
    external_ref      TEXT NOT NULL DEFAULT '',
    stellar_tx_hash   TEXT NOT NULL DEFAULT '',
    message           TEXT NOT NULL DEFAULT '',
    metadata          JSONB NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_transfers_account ON transfers (account);
CREATE INDEX IF NOT EXISTS idx_transfers_status ON transfers (status);
CREATE INDEX IF NOT EXISTS idx_transfers_account_created ON transfers (account, created_at DESC);
