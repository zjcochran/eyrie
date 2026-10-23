<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type BackupSchedule, message } from '../lib/api.ts';
  import { size } from '../lib/format.ts';
  import { api, refresh } from '../lib/session.svelte.ts';

  const histories: [string, string][] = [
    ['none', 'None, configuration only'],
    ['30', 'Last 30 days'],
    ['60', 'Last 60 days'],
    ['90', 'Last 90 days'],
    ['all', 'All of it'],
  ];

  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  // Downloading a backup.
  let history = $state('none');
  let pass = $state('');
  let pass2 = $state('');

  // Scheduled backups.
  let schedule = $state<BackupSchedule | null>(null);
  let enabled = $state(false);
  let time = $state('03:00');
  let keep = $state(14);
  let sHistory = $state('none');
  let sPass = $state('');
  let sPass2 = $state('');

  // Restoring.
  let file = $state<FileList | null>(null);
  let rPass = $state('');

  function show(s: BackupSchedule) {
    schedule = s;
    enabled = s.enabled;
    time = s.time;
    keep = s.keep;
    sHistory = s.history;
  }

  onMount(() => {
    api.backupSchedule().then(show, (e) => (notice = { ok: false, text: message(e) }));
  });

  async function run(f: () => Promise<void>) {
    busy = true;
    notice = null;
    try {
      await f();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function download(event: SubmitEvent) {
    event.preventDefault();
    if (pass !== pass2) {
      notice = { ok: false, text: 'The passphrases do not match.' };
      return;
    }
    run(async () => {
      const { blob, name } = await api.makeBackup(history, pass);
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = name;
      a.click();
      URL.revokeObjectURL(a.href);
      pass = pass2 = '';
      notice = { ok: true, text: `Saved ${name}. Keep the passphrase: without it the file cannot be restored.` };
    });
  }

  function saveSchedule(event: SubmitEvent) {
    event.preventDefault();
    if (sPass !== sPass2) {
      notice = { ok: false, text: 'The passphrases do not match.' };
      return;
    }
    run(async () => {
      show(await api.saveBackupSchedule({ enabled, time, keep, history: sHistory, passphrase: sPass }));
      sPass = sPass2 = '';
      notice = { ok: true, text: 'Saved.' };
    });
  }

  function backUpNow() {
    run(async () => {
      const s = await api.runBackup();
      show(s);
      notice = s.last?.error ? { ok: false, text: s.last.error } : { ok: true, text: `Wrote ${s.last?.file}.` };
    });
  }

  function restore(event: SubmitEvent) {
    event.preventDefault();
    const f = file?.[0];
    if (!f) return;
    if (
      !confirm(
        'Restoring replaces every monitor, setting and account with the backup’s, and signs everyone out, you too. Restore?',
      )
    ) {
      return;
    }
    run(async () => {
      await api.restore(f, rPass);
      await refresh(); // signed out: the sign-in page follows
    });
  }

  const when = (iso: string | null | undefined) => (iso ? new Date(iso).toLocaleString() : 'never');
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

<section class="card">
  <h2>Download a backup</h2>
  <p class="muted">
    One file with every monitor, setting, account and status page, and the history you choose, encrypted with a
    passphrase. It restores into any Eyrie of this version or newer, on SQLite or PostgreSQL.
  </p>
  <form onsubmit={download}>
    <label
      >History
      <select bind:value={history}>
        {#each histories as [v, l] (v)}<option value={v}>{l}</option>{/each}
      </select>
    </label>
    <label>Passphrase <input type="password" bind:value={pass} minlength="12" autocomplete="new-password" required /></label>
    <label
      >Repeat the passphrase <input type="password" bind:value={pass2} minlength="12" autocomplete="new-password" required /></label
    >
    <span class="muted help">At least 12 characters. It cannot be recovered.</span>
    <div><button class="primary" disabled={busy}>Download backup</button></div>
  </form>
</section>

<section class="card">
  <h2>Scheduled backups</h2>
  {#if schedule}
    <p class="muted">Eyrie writes a backup file every day to <span class="mono">{schedule.dir}</span>.</p>
    {#if schedule.not_mounted}
      <p class="notice error">
        This folder is not a volume of its own: its files are lost when the container is re-created. Mount a folder of
        the host there.
      </p>
    {/if}
    <form onsubmit={saveSchedule}>
      <label class="check"><input type="checkbox" bind:checked={enabled} /> Make a backup every day</label>
      <label>At (the server’s time) <input type="time" bind:value={time} required /></label>
      <label>Keep (files) <input type="number" bind:value={keep} min="1" max="365" required /></label>
      <label
        >History
        <select bind:value={sHistory}>
          {#each histories as [v, l] (v)}<option value={v}>{l}</option>{/each}
        </select>
      </label>
      <label
        >Passphrase
        <input
          type="password"
          bind:value={sPass}
          minlength="12"
          autocomplete="new-password"
          placeholder={schedule.has_passphrase ? 'Saved: leave empty to keep it' : ''}
        />
      </label>
      <label>Repeat the passphrase <input type="password" bind:value={sPass2} autocomplete="new-password" /></label>
      <div class="row">
        <button class="primary" disabled={busy}>Save</button>
        <button type="button" onclick={backUpNow} disabled={busy || !schedule.has_passphrase}>Back up now</button>
      </div>
    </form>
    {#if schedule.last}
      <p class:error-text={!!schedule.last.error}>
        Last backup {when(schedule.last.at)}:
        {schedule.last.error ? `failed: ${schedule.last.error}` : schedule.last.file}.
        {#if schedule.last.error && schedule.last.success}Last one that worked: {when(schedule.last.success)}.{/if}
      </p>
    {/if}
    {#if schedule.error}<p class="notice error">{schedule.error}</p>{/if}
    {#if schedule.files.length}
      <div class="table-wrap">
        <table>
          <thead><tr><th>File</th><th>Size</th><th>Made</th></tr></thead>
          <tbody>
            {#each schedule.files as f (f.name)}
              <tr>
                <td><a href="/api/v1/backups/{f.name}" download>{f.name}</a></td>
                <td>{size(f.size)}</td>
                <td>{when(f.time)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {:else}
    <p class="muted">Loading…</p>
  {/if}
</section>

<section class="card">
  <h2>Restore a backup</h2>
  <p class="muted">
    Replaces every monitor, setting, account and status page with the backup’s, and the history with the backup’s
    history (none, for a backup without it). Everyone is signed out; sign in with an account from the backup.
  </p>
  <form onsubmit={restore}>
    <label>Backup file <input type="file" accept=".json,application/json" bind:files={file} required /></label>
    <label>Its passphrase <input type="password" bind:value={rPass} autocomplete="off" required /></label>
    <div><button class="danger" disabled={busy}>Restore backup</button></div>
  </form>
</section>

<style>
  .help {
    margin-top: -0.5rem;
    font-size: 0.85rem;
  }
  .error-text {
    color: var(--down);
  }
</style>
