# MySQL / MariaDB

Connects to a MySQL or MariaDB database and runs a query, and is up when
it runs without an error.

![The form to add a MySQL / MariaDB monitor](../../images/monitor-types/mysql-dark.png)

## Before you start

1. **Make a user for the check**, allowed from Eyrie's address (or the
   agent's), with no rights: `SELECT 1` needs none.

   ```sql
   CREATE USER 'eyrie'@'192.0.2.20' IDENTIFIED BY 'a-long-random-password';
   ```

   To query a table, grant it that too, such as `GRANT SELECT ON
   shop.orders TO 'eyrie'@'192.0.2.20';`. A database in the connection
   string needs a right on that database.
2. MySQL must listen on the network (`bind-address` in its configuration)
   and port 3306 must be open to Eyrie.
3. **Try it** from where the check runs: `mysql -h db.example.com -u eyrie
   -p -e 'SELECT 1'`.

## Add the monitor

1. On **Monitors**, click **Add a monitor**.
2. **Type**: **MySQL / MariaDB**. **Name**: such as `Shop database`.
3. **Connection string**: such as
   `mysql://eyrie:a-long-random-password@db.example.com:3306/`. Add the
   database after the `/` only if the user has a right on it.
   Characters such as `@`, `/` or `:` in the password are written
   percent-encoded (`%40`, `%2F`, `%3A`).
4. **Query**: `SELECT 1` by default.
5. Set the [settings every type has](../monitors.md#settings-every-type-has),
   then click **Add**.

## Settings

| Setting | Default | Meaning |
| --- | --- | --- |
| Connection string | | `mysql://user:password@host:3306/database` (port 3306 by default), or the Go driver's DSN, `user:password@tcp(host:3306)/database?tls=true`; a [secret setting](../monitors.md#secret-settings) |
| Query | `SELECT 1` | Must run without an error |

- Add `?tls=true` to encrypt and check the certificate, `?tls=skip-verify`
  to encrypt without checking it.
- A connection string is checked when you save it; a saved one is kept
  until you type a new one.

## Check that it works

The message is `Query OK: 1`, `Query OK: a (3 rows)` or `Query OK, no rows`.

## Its page

- **At the top**: First value and Rows, with the uptime and the last check.
- **Query**: everything the last check found: the query, how many rows came back and the first value.
- **Chart**: the query's time, and the first value when it is a number (a `SELECT count(*)` charts the count).

With every page's outages of the last 30 days, its uptime by day and its status changes ([A monitor's page](../monitors.md#a-monitors-page)).

## When it is down

| Message | What to do |
| --- | --- |
| A message with `Access denied for user` | The password is wrong, or the user is not allowed from Eyrie's address: the host part of `'eyrie'@'…'` must match where Eyrie connects from. |
| A message with `Unknown database` | The database in the connection string does not exist, or the user has no right on it. |
| A message with `command denied` | The query reads something the user may not. |
| `Timed out`, `connection refused` | MySQL does not listen on the network, or a firewall blocks port 3306. |
