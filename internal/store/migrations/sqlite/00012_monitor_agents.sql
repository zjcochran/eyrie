-- +goose Up
-- agent_id is the agent a monitor is checked on; NULL: Eyrie itself. It is
-- not a foreign key: a monitor whose agent was removed stays on it, and
-- its checks say so, rather than moving to Eyrie unnoticed.
ALTER TABLE monitors ADD COLUMN agent_id BIGINT;
CREATE INDEX monitors_agent ON monitors (agent_id);

-- +goose Down
DROP INDEX monitors_agent;
ALTER TABLE monitors DROP COLUMN agent_id;
