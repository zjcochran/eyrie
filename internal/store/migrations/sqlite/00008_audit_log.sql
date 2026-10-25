-- +goose Up
-- Who did what: sign-ins and every change made in the app, newest last.
-- actor is the username ('' when unknown); details are key=value pairs.
CREATE TABLE audit_log (
    id      INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    at      BIGINT  NOT NULL,
    actor   TEXT    NOT NULL DEFAULT '',
    ip      TEXT    NOT NULL DEFAULT '',
    action  TEXT    NOT NULL,
    details TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at ON audit_log (at);

-- +goose Down
DROP TABLE audit_log;
