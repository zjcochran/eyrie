-- +goose Up
-- Status pages, public at /status/<slug> once published.
CREATE TABLE status_pages (
    id               BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug             TEXT    NOT NULL,
    title            TEXT    NOT NULL,
    description      TEXT    NOT NULL DEFAULT '',
    footer           TEXT    NOT NULL DEFAULT '',
    published        BOOLEAN NOT NULL DEFAULT FALSE,
    theme            TEXT    NOT NULL DEFAULT 'auto',
    accent           TEXT    NOT NULL DEFAULT '',
    show_tags        BOOLEAN NOT NULL DEFAULT FALSE,
    show_cert_expiry BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       BIGINT  NOT NULL,
    updated_at       BIGINT  NOT NULL
);
CREATE UNIQUE INDEX status_pages_slug ON status_pages (slug);

-- A page's logo, at most one.
CREATE TABLE status_page_logos (
    page_id      BIGINT NOT NULL PRIMARY KEY REFERENCES status_pages (id) ON DELETE CASCADE,
    content_type TEXT   NOT NULL,
    data         BYTEA  NOT NULL,
    updated_at   BIGINT NOT NULL
);

-- A page's sections, in order: the monitors with tag_id (if set) and the
-- monitors listed in status_page_monitors.
CREATE TABLE status_page_sections (
    id       BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_id  BIGINT  NOT NULL REFERENCES status_pages (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    title    TEXT    NOT NULL DEFAULT '',
    tag_id   BIGINT  REFERENCES tags (id) ON DELETE SET NULL
);
CREATE INDEX status_page_sections_page ON status_page_sections (page_id);
CREATE INDEX status_page_sections_tag ON status_page_sections (tag_id);

CREATE TABLE status_page_monitors (
    section_id BIGINT NOT NULL REFERENCES status_page_sections (id) ON DELETE CASCADE,
    monitor_id BIGINT NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    PRIMARY KEY (section_id, monitor_id)
);
CREATE INDEX status_page_monitors_monitor ON status_page_monitors (monitor_id);

-- Incidents on a status page. status is that of the latest update;
-- resolved_at is 0 while open.
CREATE TABLE incidents (
    id          BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_id     BIGINT  NOT NULL REFERENCES status_pages (id) ON DELETE CASCADE,
    title       TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    created_at  BIGINT  NOT NULL,
    updated_at  BIGINT  NOT NULL,
    resolved_at BIGINT  NOT NULL DEFAULT 0
);
CREATE INDEX incidents_page ON incidents (page_id, created_at);

-- An incident's timeline. author is the username of who posted it.
CREATE TABLE incident_updates (
    id          BIGINT  GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    incident_id BIGINT  NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    status      TEXT    NOT NULL,
    message     TEXT    NOT NULL,
    author      TEXT    NOT NULL DEFAULT '',
    created_at  BIGINT  NOT NULL
);
CREATE INDEX incident_updates_incident ON incident_updates (incident_id, created_at);

-- +goose Down
DROP TABLE incident_updates;
DROP TABLE incidents;
DROP TABLE status_page_monitors;
DROP TABLE status_page_sections;
DROP TABLE status_page_logos;
DROP TABLE status_pages;
