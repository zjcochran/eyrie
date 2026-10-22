-- +goose Up
-- Monitors. Settings shared by every type are columns; the type's own
-- settings are a JSON document in config. parent_id is the group a monitor
-- is in. push_token is set only for push monitors.
CREATE TABLE monitors (
    id               BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name             TEXT    NOT NULL,
    type             TEXT    NOT NULL,
    parent_id        BIGINT  REFERENCES monitors (id) ON DELETE SET NULL,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    interval_s       INTEGER NOT NULL,
    retry_interval_s INTEGER NOT NULL,
    retries          INTEGER NOT NULL,
    resend_every     INTEGER NOT NULL,
    timeout_ms       INTEGER NOT NULL,
    upside_down      BOOLEAN NOT NULL DEFAULT FALSE,
    description      TEXT    NOT NULL DEFAULT '',
    config           TEXT    NOT NULL,
    push_token       TEXT,
    created_at       BIGINT  NOT NULL,
    updated_at       BIGINT  NOT NULL
);
CREATE INDEX monitors_parent ON monitors (parent_id);
CREATE UNIQUE INDEX monitors_push_token ON monitors (push_token);

-- Each monitor's current status, kept so a restart carries on where it
-- stopped. failures counts failed checks while pending; down_checks counts
-- checks since the last down notification.
CREATE TABLE monitor_state (
    monitor_id   BIGINT  NOT NULL PRIMARY KEY REFERENCES monitors (id) ON DELETE CASCADE,
    status       TEXT    NOT NULL,
    since        BIGINT  NOT NULL,
    message      TEXT    NOT NULL DEFAULT '',
    last_check   BIGINT  NOT NULL,
    latency_ms   INTEGER NOT NULL DEFAULT -1,
    cert_expires BIGINT  NOT NULL DEFAULT 0,
    failures     INTEGER NOT NULL DEFAULT 0,
    down_checks  INTEGER NOT NULL DEFAULT 0
);

-- One row per check. latency_ms is -1 when there is none.
CREATE TABLE heartbeats (
    id         BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    monitor_id BIGINT  NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    at         BIGINT  NOT NULL,
    status     TEXT    NOT NULL,
    latency_ms INTEGER NOT NULL,
    message    TEXT    NOT NULL DEFAULT '',
    important  BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX heartbeats_monitor_at ON heartbeats (monitor_id, at);
CREATE INDEX heartbeats_at ON heartbeats (at);

-- Check counts per monitor and hour (bucket: the hour's start), for uptime
-- and response times without reading every heartbeat.
CREATE TABLE stats_hourly (
    monitor_id    BIGINT  NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    bucket        BIGINT  NOT NULL,
    up            INTEGER NOT NULL,
    down          INTEGER NOT NULL,
    latency_sum   BIGINT  NOT NULL,
    latency_count INTEGER NOT NULL,
    PRIMARY KEY (monitor_id, bucket)
);

-- The same per day (bucket: midnight UTC).
CREATE TABLE stats_daily (
    monitor_id    BIGINT  NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    bucket        BIGINT  NOT NULL,
    up            INTEGER NOT NULL,
    down          INTEGER NOT NULL,
    latency_sum   BIGINT  NOT NULL,
    latency_count INTEGER NOT NULL,
    PRIMARY KEY (monitor_id, bucket)
);

-- +goose Down
DROP TABLE stats_daily;
DROP TABLE stats_hourly;
DROP TABLE heartbeats;
DROP TABLE monitor_state;
DROP TABLE monitors;
