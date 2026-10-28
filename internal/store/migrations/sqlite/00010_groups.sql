-- +goose Up
-- Groups: folders of monitors, which can be in another group. They are not
-- checked; alert says when one notifies its own channels: 'none', 'any'
-- (the first member down) or 'selected' (one of group_alert_monitors
-- down). position orders the groups and monitors that share a parent.
-- Group monitors (type 'group', with parent_id) are turned into these
-- when the store opens.
CREATE TABLE monitor_groups (
    id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    parent_id  BIGINT  REFERENCES monitor_groups (id) ON DELETE SET NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    alert      TEXT    NOT NULL DEFAULT 'none',
    created_at BIGINT  NOT NULL,
    updated_at BIGINT  NOT NULL
);
CREATE INDEX monitor_groups_parent ON monitor_groups (parent_id);

CREATE TABLE group_channels (
    group_id   BIGINT NOT NULL REFERENCES monitor_groups (id) ON DELETE CASCADE,
    channel_id BIGINT NOT NULL REFERENCES notification_channels (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, channel_id)
);
CREATE INDEX group_channels_channel ON group_channels (channel_id);

CREATE TABLE group_alert_monitors (
    group_id   BIGINT NOT NULL REFERENCES monitor_groups (id) ON DELETE CASCADE,
    monitor_id BIGINT NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, monitor_id)
);
CREATE INDEX group_alert_monitors_monitor ON group_alert_monitors (monitor_id);

-- Maintenance windows also cover the monitors in these groups.
CREATE TABLE maintenance_groups (
    window_id BIGINT NOT NULL REFERENCES maintenance_windows (id) ON DELETE CASCADE,
    group_id  BIGINT NOT NULL REFERENCES monitor_groups (id) ON DELETE CASCADE,
    PRIMARY KEY (window_id, group_id)
);
CREATE INDEX maintenance_groups_group ON maintenance_groups (group_id);

-- The group a monitor is in (parent_id is no longer used), and its place.
ALTER TABLE monitors ADD COLUMN group_id BIGINT REFERENCES monitor_groups (id) ON DELETE SET NULL;
ALTER TABLE monitors ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
CREATE INDEX monitors_group ON monitors (group_id);

-- +goose Down
DROP INDEX monitors_group;
ALTER TABLE monitors DROP COLUMN position;
ALTER TABLE monitors DROP COLUMN group_id;
DROP TABLE maintenance_groups;
DROP TABLE group_alert_monitors;
DROP TABLE group_channels;
DROP TABLE monitor_groups;
