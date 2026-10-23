# Backup and restore

There are two ways to back up Eyrie:

- **A backup file** from **Settings → Backup and restore** (admins): every
  monitor, setting, account and status page, and history if you choose, in
  one encrypted file. It restores into any Eyrie of the same or a newer
  version, on SQLite or PostgreSQL whichever the backup came from, including
  a fresh one at setup. Use it to move Eyrie, to switch databases, or to keep
  it safe; Eyrie can write one every day by itself.
- **A copy of the database**: the data folder for SQLite, or the
  PostgreSQL database, with the secret key. Use it for your host's own
  backups.

## Backup files

### Making one

**Settings → Backup and restore → Download a backup**:

1. Choose **History**: *None, configuration only*, *Last 30 days*, *Last 60
   days*, *Last 90 days* or *All of it*.
2. Enter a passphrase of at least 12 characters, twice.
3. Click **Download backup**. The browser saves
   `eyrie-backup-YYYYMMDD-HHMMSS.json` (the time is UTC).

**Keep the passphrase.** It cannot be recovered, and without it the file
cannot be restored.

### What is in it

| Part | Contents |
| --- | --- |
| Configuration (always) | Settings (history kept, certificate warning days, single sign-on with its client secret, alert analysis with its keys, the backup schedule with its passphrase); users with their roles, disabled flags, password hashes, two-factor secrets, recovery codes (hashed), single sign-on links and profile pictures; API tokens (hashed); notification channels with their URLs, tokens and users; tags; monitors with every setting, secrets included, their channels and tags; maintenance windows; status pages with their sections, logos and incidents |
| History (optional) | Every monitor's current status, and its heartbeats, hourly and daily totals from the chosen period |

Not included: sign-in sessions, the secret key and the backup files of the
backup folder.

Heartbeats are kept only for the period in **Settings → History** (90 days
by default), hourly totals 35 days and daily totals 400 days, so a longer
range adds no older heartbeats.

The file is encrypted with the passphrase (argon2id and AES-256-GCM; see
[Security](../technical/security.md#backup-files)). Secrets that Eyrie
stores encrypted with its secret key are in the file encrypted only by the
passphrase, so the secret key is not needed to restore it. Readable without
the passphrase: that it is an Eyrie backup, the Eyrie version and database
that made it, when, and which history it holds.

### Scheduled backups

Eyrie can write a backup file every day by itself. **Settings → Backup and
restore → Scheduled backups**:

| Field | Meaning |
| --- | --- |
| Make a backup every day | On or off (off by default). |
| At | Time of day, in the server's time zone (`TZ`). Default 03:00. |
| Keep | How many files to keep, 1 to 365 (default 14). After each backup, older `eyrie-backup-*.json` files are deleted; other files in the folder are left alone. |
| History | As for a downloaded backup. |
| Passphrase | At least 12 characters, entered twice. Leave it empty to keep the saved one. Needed to turn the schedule on. |

- Files go to `/data/backups` (`EYRIE_BACKUP_DIR`), in the data volume of
  the [Compose files](installation.md), named like downloaded ones. To keep
  them off the machine's volume, mount a host folder there, such as
  `./backups:/data/backups`. Settings warns when the folder is not on a
  volume, because files in the container itself are lost when it is
  re-created.
- Eyrie looks every minute. A backup is made when the time of day has passed
  and none was made since: the first one as soon as you turn the schedule
  on, and one missed while Eyrie was off as soon as it starts.
- **Back up now** makes one at once with the saved settings.
- Each file is written under a temporary name and renamed when complete, so
  a crash never leaves a half-written backup.
- A failure (folder not writable, disk full) is logged, shown in Settings
  and not retried until the next day, or **Back up now**. The metric
  `eyrie_backup_last_success_timestamp_seconds` tells Prometheus when the
  last one worked, so you can alert on an old backup.
- Settings lists the files with their size and time; click one to download
  it.

The passphrase is stored encrypted with the secret key, so that Eyrie can
use it unattended. Someone with the backup files alone still cannot read
them; someone with the data folder (which holds the secret key) could
recover the passphrase. **Keep your own copy of the passphrase**: a backup
only helps you start over if you can type it on the new Eyrie.

### Restoring one

**Settings → Backup and restore → Restore a backup**: choose the file,
enter its passphrase, click **Restore backup** and confirm.

Restoring **replaces** everything listed under [What is in it](#what-is-in-it):
the configuration, and the monitors' status and history with the
backup's. A configuration-only backup leaves no history: the history belongs
to the monitors it replaces.

Afterwards everyone is signed out, you too; sign in with an account from the
backup. Monitors start at once with their restored status and settings, and
restored notification and alert analysis settings take effect.

A restore is refused, and nothing changes, when:

| Message | Reason |
| --- | --- |
| "Wrong passphrase, or the file is damaged." | The passphrase is not the one the file was made with, or the file was changed. |
| "This is not an Eyrie backup file." | Another kind of file. |
| "This backup was made by a newer version of Eyrie (…): upgrade Eyrie first." | Its database is newer than this Eyrie's. |
| "The backup has no enabled admin account." | Nobody could manage Eyrie afterwards. |
| "The file is larger than 128 MB." | Make a backup with less history. |

### Restoring at setup

On a fresh Eyrie, the setup page offers **Or restore a backup** below the
form that makes the first account. The restore brings back the accounts and
finishes setup; then sign in with an account from the backup. It works only
while Eyrie has no account.

### Moving Eyrie, or switching databases

1. Download a backup file, with the history you want to keep.
2. Start the new Eyrie, on SQLite or with `EYRIE_DATABASE_URL` set for
   PostgreSQL (see [Installation](installation.md)).
3. On its setup page, choose **Or restore a backup**.

## Database backups

### SQLite

Everything is in the data folder, the `eyrie-data` volume at `/data`:

| File | Contents | Needed? |
| --- | --- | --- |
| `eyrie.db` | The database | Yes |
| `eyrie.db-wal`, `eyrie.db-shm` | SQLite's write-ahead log and its index, present while Eyrie runs. Recent changes can be only in the `-wal` file. | Yes, together with the database |
| `secret.key` | The key of the stored secrets. Only made when `EYRIE_SECRET_KEY` is not set. | Yes |
| `backups/` | Scheduled backup files | Your choice |

Copy the files **together and at the same moment**:

- **Stop, copy, start**: `docker compose stop eyrie`, copy the volume's
  files, `docker compose start eyrie`. A clean stop merges the log into
  `eyrie.db`.
- **A filesystem snapshot** (ZFS, Btrfs, LVM) of the volume, without
  stopping: SQLite treats it like a power cut, consistent and recovered
  from the log when opened.

Copying `eyrie.db` alone while Eyrie runs can miss recent changes or be
corrupt. To restore: stop Eyrie, put the files back (delete any `-wal` and
`-shm` left from before when restoring a lone `eyrie.db`), and start it.

### PostgreSQL

Back up the database with PostgreSQL's own tools, such as `pg_dump`, and
keep `secret.key` from the data volume (or `EYRIE_SECRET_KEY`) with it.

### The secret key

If `secret.key` (or the `EYRIE_SECRET_KEY` value) is lost, a database
backup restores but its stored secrets cannot be read: single sign-on,
notification channels, alert analysis keys, two-factor sign-in and
monitors' secret settings need to be entered again. A backup file does not
need the key.

On start, Eyrie applies any newer database migrations, so a database backup
of an older version works with a newer Eyrie.
