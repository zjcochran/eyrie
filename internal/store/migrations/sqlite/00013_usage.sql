-- +goose Up
-- A host's CPU and memory use, from system-resources checks, in hundredths
-- of a percent (5025: 50.25%); -1: none. The stats keep their sums and how
-- many checks had them, as for response times.
ALTER TABLE heartbeats ADD COLUMN cpu_bp BIGINT NOT NULL DEFAULT -1;
ALTER TABLE heartbeats ADD COLUMN memory_bp BIGINT NOT NULL DEFAULT -1;
ALTER TABLE monitor_state ADD COLUMN cpu_bp BIGINT NOT NULL DEFAULT -1;
ALTER TABLE monitor_state ADD COLUMN memory_bp BIGINT NOT NULL DEFAULT -1;
ALTER TABLE stats_hourly ADD COLUMN cpu_bp_sum BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stats_hourly ADD COLUMN memory_bp_sum BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stats_hourly ADD COLUMN usage_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stats_daily ADD COLUMN cpu_bp_sum BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stats_daily ADD COLUMN memory_bp_sum BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stats_daily ADD COLUMN usage_count BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE stats_daily DROP COLUMN usage_count;
ALTER TABLE stats_daily DROP COLUMN memory_bp_sum;
ALTER TABLE stats_daily DROP COLUMN cpu_bp_sum;
ALTER TABLE stats_hourly DROP COLUMN usage_count;
ALTER TABLE stats_hourly DROP COLUMN memory_bp_sum;
ALTER TABLE stats_hourly DROP COLUMN cpu_bp_sum;
ALTER TABLE monitor_state DROP COLUMN memory_bp;
ALTER TABLE monitor_state DROP COLUMN cpu_bp;
ALTER TABLE heartbeats DROP COLUMN memory_bp;
ALTER TABLE heartbeats DROP COLUMN cpu_bp;
