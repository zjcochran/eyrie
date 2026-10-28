<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import TagChip from '../components/TagChip.svelte';
  import { type Schedule, type Tag, type Window, type WindowChange, message } from '../lib/api.ts';
  import { live } from '../lib/live.svelte.ts';
  import { describeSchedule, weekdayNames } from '../lib/maintenance.ts';
  import { api, app } from '../lib/session.svelte.ts';

  const admin = $derived(app.session?.user?.role === 'admin');
  let windows = $state<Window[]>([]);
  let tags = $state<Tag[]>([]);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  // The window being edited: null for none; id 0 for a new one.
  let editing = $state<Window | null>(null);
  let form = $state<WindowChange>(blank());
  // Every field of every strategy, so switching strategy keeps what was typed.
  let sched = $state<Required<Schedule>>(blankSchedule());

  const zones = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [];
  const strategies: [Schedule['strategy'], string][] = [
    ['manual', 'While turned on'],
    ['once', 'Once, between two times'],
    ['weekdays', 'On days of the week'],
    ['monthdays', 'On days of the month'],
    ['interval', 'Every so many days'],
    ['cron', 'At the times of a cron expression'],
  ];

  function blank(): WindowChange {
    return { title: '', description: '', enabled: true, schedule: blankSchedule(), all_monitors: false, monitors: [], groups: [], tags: [] };
  }

  function blankSchedule(): Required<Schedule> {
    return {
      strategy: 'once',
      time_zone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'Etc/UTC',
      start: '',
      end: '',
      from: '22:00',
      to: '02:00',
      every_days: 7,
      weekdays: [],
      month_days: [],
      cron: '0 3 * * sun',
      duration_minutes: 60,
      first_day: '',
      last_day: '',
    };
  }

  async function load() {
    [windows, tags] = await Promise.all([api.windows(), api.tags()]);
  }

  onMount(() => {
    load().catch((e) => (notice = { ok: false, text: message(e) }));
  });

  function edit(w: Window | null) {
    editing = w ?? ({ id: 0 } as Window);
    form = w ? { ...w, monitors: [...w.monitors], groups: [...w.groups], tags: [...w.tags] } : blank();
    sched = { ...blankSchedule(), ...(w?.schedule ?? {}) } as Required<Schedule>;
  }

  /** The schedule with only its strategy's fields. */
  function schedule(): Schedule {
    const s = sched;
    const out: Schedule = { strategy: s.strategy, time_zone: s.time_zone };
    const daily = { from: s.from, to: s.to };
    const days = { first_day: s.first_day || undefined, last_day: s.last_day || undefined };
    switch (s.strategy) {
      case 'once':
        return { ...out, start: s.start, end: s.end };
      case 'interval':
        return { ...out, ...daily, ...days, every_days: Number(s.every_days) };
      case 'weekdays':
        return { ...out, ...daily, ...days, weekdays: s.weekdays };
      case 'monthdays':
        return { ...out, ...daily, ...days, month_days: s.month_days };
      case 'cron':
        return { ...out, ...days, cron: s.cron, duration_minutes: Number(s.duration_minutes) };
    }
    return out;
  }

  async function run(f: () => Promise<string>) {
    busy = true;
    notice = null;
    try {
      notice = { ok: true, text: await f() };
      await load();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function save(event: SubmitEvent) {
    event.preventDefault();
    const id = editing?.id ?? 0;
    const change = { ...form, schedule: schedule() };
    run(async () => {
      const w = id ? await api.updateWindow(id, change) : await api.createWindow(change);
      editing = null;
      return `${w.title} saved.`;
    });
  }

  function toggle(w: Window) {
    run(async () => {
      const x = await api.updateWindow(w.id, { ...w, enabled: !w.enabled });
      return `${x.title} ${x.enabled ? 'turned on' : 'turned off'}.`;
    });
  }

  function remove(w: Window) {
    if (confirm(`Delete the maintenance window ${w.title}?`)) {
      run(async () => (await api.deleteWindow(w.id), `${w.title} deleted.`));
    }
  }

  const monitorName = (id: number) => live.monitors.find((m) => m.id === id)?.name ?? `#${id}`;
  const groupName = (id: number) => `${live.groups.find((g) => g.id === id)?.name ?? `#${id}`} (group)`;
  const tagOf = (id: number) => tags.find((t) => t.id === id);
  const when = (iso: string | null) => (iso ? new Date(iso).toLocaleString() : '');

  function stateText(w: Window): string {
    switch (w.state) {
      case 'on':
        return w.change ? `On until ${when(w.change)}` : 'On';
      case 'scheduled':
        return `Starts ${when(w.change)}`;
      case 'paused':
        return 'Turned off';
    }
    return 'Over';
  }

  const daily = $derived(['interval', 'weekdays', 'monthdays'].includes(sched.strategy));
  const recurring = $derived(daily || sched.strategy === 'cron');
</script>

<div class="head row">
  <h1>Maintenance</h1>
  {#if admin}<button class="primary" onclick={() => edit(null)} disabled={busy}>Add a window</button>{/if}
</div>
{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

<section class="card">
  <p class="muted">
    During a maintenance window, the monitors it covers show <em>maintenance</em>: they are still checked, but send no
    notifications, and their checks count neither for nor against uptime. When it ends, they take back the status they
    had and are checked at once.
  </p>
  {#if windows.length === 0}
    <p class="muted">No maintenance windows yet.</p>
  {:else}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Window</th><th>When</th><th>Covers</th><th>Now</th>{#if admin}<th></th>{/if}</tr></thead>
        <tbody>
          {#each windows as w (w.id)}
            <tr>
              <td>
                {w.title}
                {#if w.description}<div class="muted small">{w.description}</div>{/if}
              </td>
              <td>{describeSchedule(w.schedule)}</td>
              <td>
                {#if w.all_monitors}Every monitor{:else}
                  {[...w.groups.map(groupName), ...w.monitors.map(monitorName)].join(', ')}
                  {#each w.tags as id (id)}
                    {@const t = tagOf(id)}
                    {#if t}<TagChip name={t.name} color={t.color} />{/if}
                  {/each}
                {/if}
              </td>
              <td class:on={w.state === 'on'}>{stateText(w)}</td>
              {#if admin}
                <td>
                  <div class="row">
                    <button disabled={busy} onclick={() => toggle(w)}>{w.enabled ? 'Turn off' : 'Turn on'}</button>
                    <button disabled={busy} onclick={() => edit(w)}>Edit</button>
                    <button class="danger" disabled={busy} onclick={() => remove(w)}>Delete</button>
                  </div>
                </td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

{#if editing && admin}
  <section class="card">
    <h2>{editing.id ? `Edit ${editing.title}` : 'Add a maintenance window'}</h2>
    <form onsubmit={save}>
      <label>Title <input bind:value={form.title} maxlength="100" required placeholder="Database upgrade" /></label>
      <label>Description <input bind:value={form.description} maxlength="2000" /></label>
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> Turned on</label>

      <fieldset>
        <legend>When</legend>
        <label
          >Schedule
          <select bind:value={sched.strategy}>
            {#each strategies as [id, label] (id)}<option value={id}>{label}</option>{/each}
          </select>
        </label>
        {#if sched.strategy === 'once'}
          <label>Start <input type="datetime-local" bind:value={sched.start} required /></label>
          <label>End <input type="datetime-local" bind:value={sched.end} required /></label>
        {:else if sched.strategy === 'interval'}
          <label>Every (days) <input type="number" min="1" max="3650" bind:value={sched.every_days} required /></label>
        {:else if sched.strategy === 'weekdays'}
          <div class="days">
            {#each weekdayNames as name, i (i)}
              <label class="check"><input type="checkbox" bind:group={sched.weekdays} value={i} /> {name.slice(0, 3)}</label>
            {/each}
          </div>
        {:else if sched.strategy === 'monthdays'}
          <div class="days">
            {#each Array.from({ length: 31 }, (_, i) => i + 1) as d (d)}
              <label class="check"><input type="checkbox" bind:group={sched.month_days} value={d} /> {d}</label>
            {/each}
            <label class="check"><input type="checkbox" bind:group={sched.month_days} value={-1} /> Last day</label>
          </div>
        {:else if sched.strategy === 'cron'}
          <label
            >Cron expression <input bind:value={sched.cron} required placeholder="0 3 * * sun" />
          </label>
          <span class="muted help">Minute, hour, day of the month, month, day of the week.</span>
          <label>For (minutes) <input type="number" min="1" max="44640" bind:value={sched.duration_minutes} required /></label>
        {/if}
        {#if daily}
          <div class="row">
            <label>From <input type="time" bind:value={sched.from} required /></label>
            <label>To <input type="time" bind:value={sched.to} required /></label>
          </div>
          <span class="muted help">A window that ends at or before its start ends the next day.</span>
        {/if}
        {#if recurring}
          <div class="row">
            <label
              >{sched.strategy === 'interval' ? 'First day' : 'From the day'}
              <input type="date" bind:value={sched.first_day} required={sched.strategy === 'interval'} /></label
            >
            <label>Until the day <input type="date" bind:value={sched.last_day} /></label>
          </div>
        {/if}
        {#if sched.strategy !== 'manual'}
          <label
            >Time zone <input bind:value={sched.time_zone} list="zones" required placeholder="Etc/UTC" />
          </label>
          <datalist id="zones">{#each zones as z (z)}<option value={z}></option>{/each}</datalist>
        {/if}
      </fieldset>

      <fieldset>
        <legend>Covers</legend>
        <label class="check"><input type="checkbox" bind:checked={form.all_monitors} /> Every monitor</label>
        {#if !form.all_monitors}
          <p class="muted small">The groups (with what is in them), tags and monitors ticked.</p>
          {#if live.groups.length}
            <div class="monitors">
              {#each live.groups as g (g.id)}
                <label class="check"><input type="checkbox" bind:group={form.groups} value={g.id} /> {g.name} <span class="muted">(group)</span></label>
              {/each}
            </div>
          {/if}
          {#if tags.length}
            <div class="days">
              {#each tags as t (t.id)}
                <label class="check"><input type="checkbox" bind:group={form.tags} value={t.id} /> <TagChip name={t.name} color={t.color} /></label>
              {/each}
            </div>
          {/if}
          <div class="monitors">
            {#each live.monitors as m (m.id)}
              <label class="check"><input type="checkbox" bind:group={form.monitors} value={m.id} /> {m.name}</label>
            {/each}
          </div>
        {/if}
      </fieldset>

      <div class="row">
        <button class="primary" disabled={busy}>Save</button>
        <button type="button" onclick={() => (editing = null)}>Cancel</button>
      </div>
    </form>
  </section>
{/if}

<style>
  .head {
    justify-content: space-between;
    margin-bottom: 0.5rem;
  }
  .head h1 {
    margin: 0;
  }
  .small {
    font-size: 0.8rem;
  }
  .on {
    color: var(--maintenance);
    font-weight: 600;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    display: grid;
    gap: 0.75rem;
    padding: 0.75rem;
  }
  .days {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.9rem;
  }
  .monitors {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
    gap: 0.25rem 0.9rem;
    max-height: 16rem;
    overflow-y: auto;
  }
  .help {
    margin-top: -0.5rem;
    font-size: 0.85rem;
  }
</style>
