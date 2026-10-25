-- +goose Up
-- Agents: Eyrie's agents on other hosts, which report the workloads they
-- see. token_hash is the SHA-256 of a one-time enrollment token ('' when
-- none waits); public_key is '' until the agent enrolls. previous_key is
-- accepted until the agent signs with the new one. workloads is the last
-- report's list, as JSON; error is why the agent could not list them.
CREATE TABLE agents (
    id            INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL,
    platform      TEXT    NOT NULL DEFAULT '',
    hostname      TEXT    NOT NULL DEFAULT '',
    version       TEXT    NOT NULL DEFAULT '',
    token_hash    TEXT    NOT NULL DEFAULT '',
    token_expires BIGINT  NOT NULL DEFAULT 0,
    public_key    TEXT    NOT NULL DEFAULT '',
    previous_key  TEXT    NOT NULL DEFAULT '',
    key_created   BIGINT  NOT NULL DEFAULT 0,
    last_seen     BIGINT  NOT NULL DEFAULT 0,
    last_address  TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT '',
    workloads     TEXT    NOT NULL DEFAULT '[]',
    created_at    BIGINT  NOT NULL
);
CREATE UNIQUE INDEX agents_name ON agents (name);

-- +goose Down
DROP TABLE agents;
