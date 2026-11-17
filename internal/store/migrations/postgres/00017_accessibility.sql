-- +goose Up
-- Each user's accessibility: the accessibility mode, the colours for their
-- colour vision, and the text size in percent.
ALTER TABLE users ADD COLUMN accessible BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN vision TEXT NOT NULL DEFAULT 'typical';
ALTER TABLE users ADD COLUMN text_size INTEGER NOT NULL DEFAULT 100;

-- +goose Down
ALTER TABLE users DROP COLUMN text_size;
ALTER TABLE users DROP COLUMN vision;
ALTER TABLE users DROP COLUMN accessible;
