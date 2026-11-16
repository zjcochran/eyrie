-- +goose Up
-- Every monitor's stats of the latest hours and days, for their uptime,
-- without reading the older ones.
CREATE INDEX stats_hourly_bucket ON stats_hourly (bucket);
CREATE INDEX stats_daily_bucket ON stats_daily (bucket);

-- +goose Down
DROP INDEX stats_daily_bucket;
DROP INDEX stats_hourly_bucket;
