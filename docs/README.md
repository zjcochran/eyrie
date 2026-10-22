# Eyrie documentation

Eyrie is a self-hosted uptime and infrastructure monitor. These documents
state exactly how it behaves: every setting, route, table and limit. They
are updated in the same change as the behaviour they describe.

## User guides

| Guide | What it covers |
| --- | --- |
| [Installation](user/installation.md) | Building the image, Docker Compose with SQLite or PostgreSQL, the data folder, upgrading |

## Technical documentation

| Document | What it covers |
| --- | --- |
| [Architecture](technical/architecture.md) | Packages, how the web app is built and embedded, startup and shutdown |
| [Configuration reference](technical/configuration-reference.md) | Every environment variable, with defaults and limits |
| [HTTP interface](technical/http-interface.md) | Every route, response headers, caching |
| [Data model](technical/data-model.md) | Both databases, migrations, every table and column |
| [Testing and CI](technical/testing-and-ci.md) | Test rules, the GitLab pipeline, its reports and gates, releases, the weekly dependency check |
| [Development](technical/development.md) | Building, checks, code conventions, adding migrations and npm packages |
