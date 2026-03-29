CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- User alert preferences
CREATE TABLE IF NOT EXISTS user_configs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    token       VARCHAR(255) UNIQUE NOT NULL,
    magnitude   DECIMAL(4,1) NOT NULL DEFAULT 0.0,
    distance_km INTEGER      NOT NULL DEFAULT 100,
    channel     VARCHAR(10)  NOT NULL DEFAULT 'fcm', -- apns | fcm
    status      VARCHAR(20)  NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Idempotency gate + status ledger for every device-level notification
CREATE TABLE IF NOT EXISTS notification_outbox (
    notification_id VARCHAR(64)  PRIMARY KEY,          -- SHA-256(alert_id|version|device_id)
    alert_id        VARCHAR(255) NOT NULL,
    version         INTEGER      NOT NULL,
    device_id       VARCHAR(255) NOT NULL,
    channel         VARCHAR(10)  NOT NULL,
    status          VARCHAR(30)  NOT NULL DEFAULT 'ENQUEUED',
    -- ENQUEUED | ATTEMPTED | VENDOR_ACCEPTED | FAILED | CANCELLED_SUPERSEDED
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_alert_version
    ON notification_outbox(alert_id, version);

-- Partial index for fast lookup of non-terminal records
CREATE INDEX IF NOT EXISTS idx_outbox_active
    ON notification_outbox(alert_id)
    WHERE status NOT IN ('VENDOR_ACCEPTED', 'FAILED', 'CANCELLED_SUPERSEDED');
