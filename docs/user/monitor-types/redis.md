# Redis

Connects to Redis, signs in, and sends `PING`; up when it answers `PONG`.
Works with anything that speaks Redis's protocol, such as Valkey.

![The form to add a Redis monitor](../../images/monitor-types/redis-dark.png)

## Before you start

- Redis must listen on the network (`bind` in `redis.conf`) and port 6379
  must be open to Eyrie (or the agent).
- If Redis has a password (`requirepass`), have it ready. With Redis 6's
  ACLs, a user that can only `PING` (and `SELECT` a database other than
  0) is enough:

  ```text
  ACL SETUSER eyrie on >a-long-random-password -@all +ping +select
  ```

- **Try it**: `redis-cli -h cache.example.com --user eyrie --pass
  a-long-random-password ping`.

## Add the monitor

1. On **Monitors**, click **Add a monitor**.
2. **Type**: **Redis**. **Name**: such as `Cache`.
3. **Connection string**:
   - no password: `redis://cache.example.com:6379`;
   - a password: `redis://:a-long-random-password@cache.example.com:6379`;
   - an ACL user: `redis://eyrie:a-long-random-password@cache.example.com:6379`;
   - TLS: `rediss://…`;
   - a database other than 0: `/1` at the end.
4. Set the [settings every type has](../monitors.md#settings-every-type-has),
   then click **Add**.

## Settings

| Setting | Default | Meaning |
| --- | --- | --- |
| Connection string | | `redis://host[:port][/db]` (port 6379), or `rediss://` for TLS, with `user:password@` or `:password@` before the host; a [secret setting](../monitors.md#secret-settings) |
| Ignore TLS certificate errors | off | For `rediss://` |

## Check that it works

The message is `PONG`.

## Its page

- **At the top**: the database, the response time, the uptime and the last check.
- **Server**: everything the last check found: the reply, the database, whether TLS is on, and the user.
- **Chart**: response time.

With every page's outages of the last 30 days, its uptime by day and its status changes ([A monitor's page](../monitors.md#a-monitors-page)).

## When it is down

| Message | What to do |
| --- | --- |
| `NOAUTH Authentication required.` | Redis has a password: add it to the connection string. |
| A message starting `WRONGPASS` | The user name or password is wrong. |
| A message starting `NOPERM` | The ACL user may not run `PING` or `SELECT`: allow them. |
| `Timed out`, `connection refused` | Redis listens only on its own host (`bind 127.0.0.1`), runs in protected mode, or a firewall blocks it. |
