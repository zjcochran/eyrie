-- +goose Up
-- Settings saved from the web UI, one JSON document per key. Times in every
-- table are Unix milliseconds.
CREATE TABLE settings (
    key        TEXT    NOT NULL PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at BIGINT  NOT NULL
);

-- +goose Down
DROP TABLE settings;
