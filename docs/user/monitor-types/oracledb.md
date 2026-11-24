# Oracle Database

Connects to an Oracle database and runs a query, and is up when it runs
without an error.

![The form to add an Oracle Database monitor](../../images/monitor-types/oracledb-dark.png)

## Before you start

1. **Make a user for the check** in the pluggable database, with only the
   right to connect:

   ```sql
   CREATE USER eyrie IDENTIFIED BY "a-long-random-password";
   GRANT CREATE SESSION TO eyrie;
   ```

   To query a table, grant it that too, such as `GRANT SELECT ON
   shop.orders TO eyrie;`.
2. Find the **service name** of the database (not its SID): `SELECT
   value FROM v$parameter WHERE name = 'service_names';`, or
   `lsnrctl status` on the server. For Oracle Database Free it is
   `FREEPDB1`.
3. The listener's port, 1521 by default, must be open to Eyrie (or the
   agent).

## Add the monitor

1. On **Monitors**, click **Add a monitor**.
2. **Type**: **Oracle Database**. **Name**: such as `Ledger database`.
3. **Connection string**: such as
   `oracle://eyrie:a-long-random-password@db.example.com:1521/FREEPDB1`.
   Characters such as `@`, `/` or `:` in the password are written
   percent-encoded (`%40`, `%2F`, `%3A`).
4. **Query**: `SELECT 1 FROM DUAL` by default.
5. Set the [settings every type has](../monitors.md#settings-every-type-has),
   then click **Add**.

## Settings

| Setting | Default | Meaning |
| --- | --- | --- |
| Connection string | | `oracle://user:password@host:1521/service`; a [secret setting](../monitors.md#secret-settings) |
| Query | `SELECT 1 FROM DUAL` | Must run without an error |

Eyrie speaks Oracle's protocol itself: no Oracle client is needed.

## Check that it works

The message is `Query OK: 1`.

## Its page

- **At the top**: the first value, the response time, the uptime and the last check.
- **Query**: everything the last check found: the query, how many rows came back and the first value.
- **Chart**: the query's time, and the first value when it is a number (a `SELECT count(*)` charts the count).

With every page's outages of the last 30 days, its uptime by day and its status changes ([A monitor's page](../monitors.md#a-monitors-page)).

## When it is down

| Message | What to do |
| --- | --- |
| A message with `ORA-01017` | The user name or password is wrong. |
| A message with `ORA-12514` | The listener does not know that service name: check it with `lsnrctl status`. |
| A message with `ORA-01045` | The user lacks `CREATE SESSION`. |
| A message with `ORA-00942` | The query reads a table that does not exist, or that the user may not read. |
| `Timed out`, `connection refused` | The listener does not run on that host and port, or a firewall blocks it. |
