# MongoDB

Connects to MongoDB and runs a database command, and is up when it
succeeds, and if you want, when a value in its answer is right: a
replica set member's health, a server's state.

![The form to add a MongoDB monitor](../../images/monitor-types/mongodb-dark.png)

## Before you start

1. **Make a user for the check**. `{"ping": 1}` needs no rights; status
   commands such as `replSetGetStatus` and `serverStatus` need the
   `clusterMonitor` role. In `mongosh`:

   ```js
   db.getSiblingDB("admin").createUser({
     user: "eyrie",
     pwd: "a-long-random-password",
     roles: [{ role: "clusterMonitor", db: "admin" }]
   })
   ```

2. MongoDB must listen on the network (`net.bindIp` in `mongod.conf`), and
   port 27017 must be open to Eyrie (or the agent).
3. **Try it**: `mongosh "mongodb://eyrie@db.example.com:27017/?authSource=admin"
   --eval 'db.runCommand({ping: 1})'`.

## Add the monitor

1. On **Monitors**, click **Add a monitor**.
2. **Type**: **MongoDB**. **Name**: such as `Shop database`.
3. **Connection string**: such as
   `mongodb://eyrie:a-long-random-password@db.example.com:27017/?authSource=admin`,
   or `mongodb+srv://…` for a cluster published in DNS. Characters such as
   `@`, `/` or `:` in the password are written percent-encoded.
4. **Command (JSON)**: `{"ping": 1}` by default.
5. To check a value in the answer, a **JSON path**, **Comparison** and
   **Expected value**, as for [HTTP(S) JSON query](json-query.md#writing-the-path).
   For example, command `{"replSetGetStatus": 1}` with path
   `members[0].health`, `==` and `1`.
6. Set the [settings every type has](../monitors.md#settings-every-type-has),
   then click **Add**.

## Settings

| Setting | Default | Meaning |
| --- | --- | --- |
| Connection string | | `mongodb://` or `mongodb+srv://`; a [secret setting](../monitors.md#secret-settings) |
| Command (JSON) | `{"ping": 1}` | MongoDB's extended JSON; run on the URI's database, or `admin` |
| JSON path | | Optional: a value in the answer |
| Comparison | `==` | As for JSON queries |
| Expected value | | |

## Check that it works

The message is `OK:` and the start of the answer, or with a path, the
value found: `members[0].health is 1`.

## Its page

- **At the top**: the value, the response time, the uptime and the last check.
- **Command**: everything the last check found: the command, and the answer or the value at the path.
- **Chart**: the command's time, and the value when it is a number.

With every page's outages of the last 30 days, its uptime by day and its status changes ([A monitor's page](../monitors.md#a-monitors-page)).

## When it is down

| Message | What to do |
| --- | --- |
| A message with `Authentication failed` | The user name or password is wrong, or `authSource` is not the database the user was made in (`admin` above). |
| A message with `not authorized` | The user lacks the role the command needs, such as `clusterMonitor`. |
| `members[0].health is 0, not == 1` | The value is not what you expected: here, that member is down. |
| A message with `server selection` or `Timed out` | Eyrie could not reach a server: MongoDB does not listen on the network, a firewall blocks it, or a replica set's members are named in a way Eyrie cannot resolve (`directConnection=true` in the URI connects to the one host only). |
