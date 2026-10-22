-- +goose Up
-- Accounts and sign-in. Usernames are unique ignoring case: username_key is
-- the lower-cased name. password_hash is NULL for accounts that sign in only
-- with single sign-on. totp_secret is sealed with the instance key ('' when
-- two-factor sign-in is off); totp_step is the last time step accepted, so a
-- code works once.
CREATE TABLE users (
    id            INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL,
    username_key  TEXT    NOT NULL,
    password_hash TEXT,
    role          TEXT    NOT NULL CHECK (role IN ('admin', 'viewer')),
    disabled      BOOLEAN NOT NULL DEFAULT FALSE,
    totp_secret   TEXT    NOT NULL DEFAULT '',
    totp_step     BIGINT  NOT NULL DEFAULT 0,
    sso_picture   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    BIGINT  NOT NULL,
    last_login    BIGINT  NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX users_username_key ON users (username_key);

-- Signed-in browsers. id is the SHA-256 of the cookie token, never the token.
CREATE TABLE sessions (
    id         TEXT    NOT NULL PRIMARY KEY,
    user_id    BIGINT  NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf       TEXT    NOT NULL,
    created_at BIGINT  NOT NULL,
    expires_at BIGINT  NOT NULL,
    ip         TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions (user_id);

-- Single sign-on accounts linked to users, matched on issuer and subject.
CREATE TABLE oidc_identities (
    issuer    TEXT    NOT NULL,
    subject   TEXT    NOT NULL,
    user_id   BIGINT  NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email     TEXT    NOT NULL DEFAULT '',
    linked_at BIGINT  NOT NULL,
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX oidc_identities_user ON oidc_identities (user_id);

-- Two-factor recovery codes, as SHA-256 hashes, deleted when used.
CREATE TABLE recovery_codes (
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    hash    TEXT   NOT NULL,
    PRIMARY KEY (user_id, hash)
);

-- Profile pictures, uploaded or fetched from single sign-on. An uploaded
-- picture is never replaced by the provider's.
CREATE TABLE user_pictures (
    user_id      BIGINT NOT NULL PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    content_type TEXT   NOT NULL,
    data         BLOB   NOT NULL,
    source       TEXT   NOT NULL CHECK (source IN ('upload', 'sso')),
    updated_at   BIGINT NOT NULL
);

-- Tokens for the API. Only a SHA-256 hash of each token is kept.
CREATE TABLE api_tokens (
    id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    hash       TEXT    NOT NULL,
    created_at BIGINT  NOT NULL,
    created_by TEXT    NOT NULL DEFAULT '',
    last_used  BIGINT  NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX api_tokens_hash ON api_tokens (hash);

-- +goose Down
DROP TABLE api_tokens;
DROP TABLE user_pictures;
DROP TABLE recovery_codes;
DROP TABLE oidc_identities;
DROP TABLE sessions;
DROP TABLE users;
