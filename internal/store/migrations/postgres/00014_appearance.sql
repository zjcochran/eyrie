-- +goose Up
-- How the web app looks for each user: its layout, the layout's colour
-- scheme, and day, night or the system's choice.
ALTER TABLE users ADD COLUMN layout TEXT NOT NULL DEFAULT 'console';
ALTER TABLE users ADD COLUMN scheme TEXT NOT NULL DEFAULT 'ash';
ALTER TABLE users ADD COLUMN mode TEXT NOT NULL DEFAULT 'system';

-- +goose Down
ALTER TABLE users DROP COLUMN mode;
ALTER TABLE users DROP COLUMN scheme;
ALTER TABLE users DROP COLUMN layout;
