-- +goose Up
-- Every monitor's status changes, newest first, without reading every
-- heartbeat.
CREATE INDEX heartbeats_important ON heartbeats (important, id);

-- +goose Down
DROP INDEX heartbeats_important;
