# Microsoft SQL Server

Connects to a SQL Server database and runs a query, and is up when it runs
without an error.

![The form to add a Microsoft SQL Server monitor](../../images/monitor-types/sqlserver-dark.png)

## Before you start

1. **Make a SQL Server login for the check** (SQL Server authentication
   must be on: **Server properties → Security → SQL Server and Windows
   Authentication mode**):

   ```sql
   CREATE LOGIN eyrie WITH PASSWORD = 'A-long-random-password-1';
   ```

   `SELECT 1` needs nothing more. To query a database, add a user there
   with the rights it needs:

   ```sql
   USE shop;
   CREATE USER eyrie FOR LOGIN eyrie;
   GRANT SELECT ON dbo.orders TO eyrie;
   ```

2. TCP/IP must be on (**SQL Server Configuration Manager**), and port 1433
   open to Eyrie (or the agent).

## Add the monitor

1. On **Monitors**, click **Add a monitor**.
2. **Type**: **Microsoft SQL Server**. **Name**: such as `ERP database`.
3. **Connection string**: such as
   `sqlserver://eyrie:A-long-random-password-1@db.example.com:1433?database=shop`.
   Characters such as `@`, `/` or `:` in the password are written
   percent-encoded (`%40`, `%2F`, `%3A`).
4. **Query**: `SELECT 1` by default.
5. Set the [settings every type has](../monitors.md#settings-every-type-has),
   then click **Add**.

## Settings

| Setting | Default | Meaning |
| --- | --- | --- |
| Connection string | | `sqlserver://user:password@host:1433?database=name`, or `server=…;user id=…;password=…;database=…`; a [secret setting](../monitors.md#secret-settings) |
| Query | `SELECT 1` | Must run without an error |

- A server with a self-signed certificate needs
  `&TrustServerCertificate=true` at the end of the URL.
- A named instance: `sqlserver://user:password@host/INSTANCE`.
- A connection string is checked when you save it; a saved one is kept
  until you type a new one.

## Check that it works

The message is `Query OK: 1`, `Query OK: a (3 rows)` or `Query OK, no rows`.

## When it is down

| Message | What to do |
| --- | --- |
| A message with `Login failed for user` | The login or password is wrong, or SQL Server authentication is off. |
| A message with `certificate` or `x509` | The server's certificate is not trusted: add `TrustServerCertificate=true`, or give SQL Server a certificate Eyrie trusts. |
| A message with `Cannot open database` | The database does not exist, or the login has no user in it. |
| `Timed out`, `connection refused` | TCP/IP is off, the port is another, or a firewall blocks it. |
