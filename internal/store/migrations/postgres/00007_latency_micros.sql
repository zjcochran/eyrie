-- +goose Up
-- Response times in microseconds: in whole milliseconds a ping that takes
-- 0.5 ms was saved as 0. latency_ms and latency_sum stay, still written,
-- so a backup from before this has what restoring needs; a negative
-- latency_us_sum means "not known yet" and is filled from latency_sum.
ALTER TABLE monitor_state ADD COLUMN latency_us BIGINT NOT NULL DEFAULT -1;
ALTER TABLE heartbeats ADD COLUMN latency_us BIGINT NOT NULL DEFAULT -1;
ALTER TABLE stats_hourly ADD COLUMN latency_us_sum BIGINT NOT NULL DEFAULT -1;
ALTER TABLE stats_daily ADD COLUMN latency_us_sum BIGINT NOT NULL DEFAULT -1;
UPDATE monitor_state SET latency_us = latency_ms * 1000 WHERE latency_us < 0 AND latency_ms >= 0;
UPDATE heartbeats SET latency_us = latency_ms * 1000 WHERE latency_us < 0 AND latency_ms >= 0;
UPDATE stats_hourly SET latency_us_sum = latency_sum * 1000 WHERE latency_us_sum < 0;
UPDATE stats_daily SET latency_us_sum = latency_sum * 1000 WHERE latency_us_sum < 0;

-- +goose Down
ALTER TABLE stats_daily DROP COLUMN latency_us_sum;
ALTER TABLE stats_hourly DROP COLUMN latency_us_sum;
ALTER TABLE heartbeats DROP COLUMN latency_us;
ALTER TABLE monitor_state DROP COLUMN latency_us;
