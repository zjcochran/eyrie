-- +goose Up
-- What a monitor's last check found, by name, as a JSON array of
-- {"name", "value"}: a container's state, a game server's players, WAN
-- Drawbridge's ISP. Its page shows them; '' for none.
ALTER TABLE monitor_state ADD COLUMN details TEXT NOT NULL DEFAULT '';
-- The number a check measured, for its chart (a clock's offset, a game
-- server's players, an SNMP value); NULL: none. The stats keep their sum
-- and how many checks had one, as for response times.
ALTER TABLE heartbeats ADD COLUMN value DOUBLE PRECISION;
ALTER TABLE stats_hourly ADD COLUMN value_sum DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE stats_hourly ADD COLUMN value_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stats_daily ADD COLUMN value_sum DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE stats_daily ADD COLUMN value_count BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE stats_daily DROP COLUMN value_count;
ALTER TABLE stats_daily DROP COLUMN value_sum;
ALTER TABLE stats_hourly DROP COLUMN value_count;
ALTER TABLE stats_hourly DROP COLUMN value_sum;
ALTER TABLE heartbeats DROP COLUMN value;
ALTER TABLE monitor_state DROP COLUMN details;
