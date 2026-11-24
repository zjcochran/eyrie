# PostgreSQL

Connects to a PostgreSQL database and runs a query, and is up when it runs
without an error.

![The form to add a PostgreSQL monitor](../../images/monitor-types/postgres-dark.png)

## Before you start

1. **Make a user for the check**, with no rights beyond connecting. As a
   superuser, in `psql`:

   ```sql
   CREATE ROLE eyrie LOGIN PASSWORD 'a-long-random-password';
   GRANT CONNECT ON DATABASE shop TO eyrie;
   ```

   To query a table, grant it that too, such as `GRANT SELECT ON orders
   TO eyrie;`. To read replication or activity views, `GRANT pg_monitor
   TO eyrie;`.
2. **Allow it to connect from Eyrie's address** (or the agent's): a line
   in `pg_hba.conf`, then reload PostgreSQL.

   ```text
   host  shop  eyrie  192.0.2.20/32  scram-sha-256
   ```

   And `listen_addresses` in `postgresql.conf` must include the address
   Eyrie connects to.
3. **Try it** from where the check runs: `psql
   "postgres://eyrie@db.example.com:5432/shop" -c 'SELECT 1'`.

## Add the monitor

1. On **Monitors**, click **Add a monitor**.
2. **Type**: **PostgreSQL**. **Name**: such as `Shop database`.
3. **Connection string**: such as
   `postgres://eyrie:a-long-random-password@db.example.com:5432/shop?sslmode=require`.
   Characters such as `@`, `/` or `:` in the password are written
   percent-encoded (`%40`, `%2F`, `%3A`).
4. **Query**: `SELECT 1` by default. The query runs as written, so give
   the user only the rights it needs.
5. Set the [settings every type has](../monitors.md#settings-every-type-has),
   then click **Add**.

## Settings

| Setting | Default | Meaning |
| --- | --- | --- |
| Connection string | | A URL, `postgres://user:password@host:5432/database?sslmode=require`, or `host=… user=… password=… dbname=…`; a [secret setting](../monitors.md#secret-settings) |
| Query | `SELECT 1` | Must run without an error; at most 10000 characters |

- `sslmode`: `require` encrypts without checking the certificate,
  `verify-full` checks it too, `disable` turns TLS off.
- Queries use the simple protocol, which works through connection poolers
  such as PgBouncer.
- A connection string is checked when you save it; a saved one is kept
  until you type a new one.

## Check that it works

The message gives the first column of the first row, and how many rows
came back (counted up to 1000): `Query OK: 1`, `Query OK: a (3 rows)`,
`Query OK, no rows`.

## When it is down

| Message | What to do |
| --- | --- |
| A message with `password authentication failed` | The user name or password is wrong. |
| A message with `no pg_hba.conf entry` | PostgreSQL does not allow this user from Eyrie's address: add the line above and reload. |
| A message with `does not exist` | The database (or a table in the query) does not exist, or the user cannot see it. |
| A message with `permission denied` | The query reads something the user may not: grant it, or change the query. |
| `Timed out`, `connection refused` | PostgreSQL does not listen on that address and port, or a firewall blocks it. |
