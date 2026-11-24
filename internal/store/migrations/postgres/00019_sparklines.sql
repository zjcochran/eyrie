-- +goose Up
-- Whether the list of monitors shows a line of each one's response times
-- (the Console layout); on by default.
ALTER TABLE users ADD COLUMN sparklines BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE users DROP COLUMN sparklines;
