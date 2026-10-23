-- +goose Up
-- Tags: a name (unique, whatever the case) and a colour such as #1f5fbf.
CREATE TABLE tags (
    id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    color      TEXT    NOT NULL,
    created_at BIGINT  NOT NULL,
    updated_at BIGINT  NOT NULL
);
CREATE UNIQUE INDEX tags_name ON tags (LOWER(name));

-- The tags of each monitor, each with an optional value (env: prod).
CREATE TABLE monitor_tags (
    monitor_id BIGINT NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    tag_id     BIGINT NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    value      TEXT   NOT NULL DEFAULT '',
    PRIMARY KEY (monitor_id, tag_id)
);
CREATE INDEX monitor_tags_tag ON monitor_tags (tag_id);

-- Maintenance windows. schedule is a JSON document that package
-- maintenance reads. A window covers every monitor (all_monitors), or the
-- monitors and the monitors with the tags listed below, and the members
-- of the groups among them.
CREATE TABLE maintenance_windows (
    id           INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    title        TEXT    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    schedule     TEXT    NOT NULL,
    all_monitors BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   BIGINT  NOT NULL,
    updated_at   BIGINT  NOT NULL
);

CREATE TABLE maintenance_monitors (
    window_id  BIGINT NOT NULL REFERENCES maintenance_windows (id) ON DELETE CASCADE,
    monitor_id BIGINT NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    PRIMARY KEY (window_id, monitor_id)
);
CREATE INDEX maintenance_monitors_monitor ON maintenance_monitors (monitor_id);

CREATE TABLE maintenance_tags (
    window_id BIGINT NOT NULL REFERENCES maintenance_windows (id) ON DELETE CASCADE,
    tag_id    BIGINT NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (window_id, tag_id)
);
CREATE INDEX maintenance_tags_tag ON maintenance_tags (tag_id);

-- During maintenance, the status the monitor had before it began, which
-- comes back when it ends; '' outside maintenance.
ALTER TABLE monitor_state ADD COLUMN before_status TEXT NOT NULL DEFAULT '';
ALTER TABLE monitor_state ADD COLUMN before_since BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE monitor_state DROP COLUMN before_since;
ALTER TABLE monitor_state DROP COLUMN before_status;
DROP TABLE maintenance_tags;
DROP TABLE maintenance_monitors;
DROP TABLE maintenance_windows;
DROP TABLE monitor_tags;
DROP TABLE tags;
