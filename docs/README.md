# Eyrie documentation

Eyrie is a self-hosted uptime and infrastructure monitor. These documents
state exactly how it behaves: every setting, route, table and limit. They
are updated in the same change as the behaviour they describe.

## User guides

| Guide | What it covers |
| --- | --- |
| [Installation](user/installation.md) | Building the image, Docker Compose with SQLite or PostgreSQL, the data folder, upgrading |
| [Monitors](user/monitors.md) | The dashboard, a monitor's page, adding monitors, every type and setting, push URLs, uptime and history |
| [Notifications](user/notifications.md) | Channels (ntfy, Gotify, Pushover, Telegram, Discord, email, signed webhooks), what is sent, certificate warnings, delivery |
| [Using the web app](user/web-ui.md) | Setup, signing in, your account, two-factor sign-in, users, API tokens |
| [Single sign-on](user/single-sign-on.md) | Signing in with an OpenID Connect provider: setup, groups, linking, SSO only |

## Technical documentation

| Document | What it covers |
| --- | --- |
| [Architecture](technical/architecture.md) | Packages, the monitor engine, how the web app is built and embedded, startup and shutdown |
| [Configuration reference](technical/configuration-reference.md) | Every environment variable, with defaults and limits; the command line |
| [HTTP interface](technical/http-interface.md) | Every route, the event stream, Prometheus metrics, response headers, caching |
| [Security](technical/security.md) | Passwords, two-factor sign-in, sessions, CSRF, roles, API tokens, proxies, single sign-on, stored secrets |
| [Data model](technical/data-model.md) | Both databases, migrations, every table and column |
| [Testing and CI](technical/testing-and-ci.md) | Test rules, the GitLab pipeline, its reports and gates, releases, the weekly dependency check |
| [Development](technical/development.md) | Building, checks, code conventions, adding migrations and npm packages |
