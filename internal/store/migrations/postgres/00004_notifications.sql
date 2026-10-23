-- +goose Up
-- Notification channels. url, token and username are sealed with the
-- instance key: a webhook URL can itself be a credential. is_default
-- channels are given to new monitors.
CREATE TABLE notification_channels (
    id         BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT    NOT NULL,
    kind       TEXT    NOT NULL,
    url        TEXT    NOT NULL DEFAULT '',
    token      TEXT    NOT NULL DEFAULT '',
    username   TEXT    NOT NULL DEFAULT '',
    recipients TEXT    NOT NULL DEFAULT '',
    sender     TEXT    NOT NULL DEFAULT '',
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at BIGINT  NOT NULL,
    updated_at BIGINT  NOT NULL
);

-- Which channels each monitor notifies.
CREATE TABLE monitor_channels (
    monitor_id BIGINT NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    channel_id BIGINT NOT NULL REFERENCES notification_channels (id) ON DELETE CASCADE,
    PRIMARY KEY (monitor_id, channel_id)
);
CREATE INDEX monitor_channels_channel ON monitor_channels (channel_id);

-- The certificate expiry warning last sent for the certificate in
-- cert_expires: its threshold in days; 0 for none yet.
ALTER TABLE monitor_state ADD COLUMN cert_notified INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE monitor_state DROP COLUMN cert_notified;
DROP TABLE monitor_channels;
DROP TABLE notification_channels;
