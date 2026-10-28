<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import StatusDot from '../components/StatusDot.svelte';
  import { type Channel, type GroupAlert, type GroupChange, message } from '../lib/api.ts';
  import { live, loadMonitors } from '../lib/live.svelte.ts';
  import { go, link } from '../lib/nav.svelte.ts';
  import { api } from '../lib/session.svelte.ts';
  import { buildTree, findGroup, groupChoices, groupStatus, members } from '../lib/tree.ts';

  let { id, admin = false }: { id: number; admin?: boolean } = $props();

  const tree = $derived(buildTree(live.groups, live.monitors));
  const item = $derived(findGroup(tree, id));
  const group = $derived(item?.group ?? null);
  const status = $derived(item ? groupStatus(item) : null);
  const inside = $derived(item ? members(item) : []);
  // Where it can go: the top level, or a group outside it.
  const parents = $derived(groupChoices(tree, id));

  let form = $state<GroupChange | null>(null);
  let channels = $state<Channel[]>([]);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  // The form starts from the saved group, once it is loaded.
  $effect(() => {
    if (group && !form) {
      form = {
        name: group.name,
        parent_id: group.parent_id,
        alert: group.alert,
        channels: [...group.channels],
        alert_monitors: [...group.alert_monitors],
      };
    }
  });
  $effect(() => {
    if (admin) api.channels().then((cs) => (channels = cs), (e) => (notice = { ok: false, text: message(e) }));
  });

  const alerts: { value: GroupAlert; label: string }[] = [
    { value: 'none', label: 'No alerts: only its monitors alert, each on its own channels' },
    { value: 'any', label: 'When its first monitor goes down, and when they are all back up' },
    { value: 'selected', label: 'When one of the monitors chosen below goes down, and when they are all back up' },
  ];

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!form) return;
    busy = true;
    notice = null;
    try {
      await api.updateGroup(id, { ...form, parent_id: form.parent_id || null });
      await loadMonitors();
      notice = { ok: true, text: 'Saved.' };
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  async function remove() {
    if (!group) return;
    const to = group.parent_id ? (live.groups.find((g) => g.id === group.parent_id)?.name ?? 'its parent group') : 'the top level';
    if (!confirm(`Delete the group ${group.name}? What is in it moves to ${to}; no monitor is deleted.`)) return;
    busy = true;
    try {
      await api.deleteGroup(id);
      await loadMonitors();
      go('/');
    } catch (e) {
      notice = { ok: false, text: message(e) };
      busy = false;
    }
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

{#if !live.loaded}
  <p class="muted">Loading…</p>
{:else if !group || !status}
  <h1>Not found</h1>
  <p>There is no such group. <a href="/" onclick={link}>Back to the monitors</a></p>
{:else}
  <div class="head">
    <div>
      <h1><StatusDot status={status.status} /> {group.name}</h1>
      <p class="muted">
        Group · {status.total} active {status.total === 1 ? 'monitor' : 'monitors'}: {status.up} up{#if status.down}, {status.down} down{/if}
        · {group.alert === 'none' ? 'no alerts' : 'alerts its own channels'}
      </p>
    </div>
    {#if admin}<button class="danger" disabled={busy} onclick={remove}>Delete group</button>{/if}
  </div>

  <section class="card">
    <h2>Monitors in it</h2>
    {#if inside.length === 0}
      <p class="muted">None yet.{#if admin} Drag monitors onto the group in the list, or choose it as a monitor's group.{/if}</p>
    {:else}
      <ul class="members">
        {#each inside as m (m.id)}
          <li>
            <StatusDot status={m.status.status} active={m.active} />
            <a href="/monitors/{m.id}" onclick={link}>{m.name}</a>
            <span class="muted msg">{m.status.message}</span>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  {#if admin && form}
    <section class="card">
      <h2>Settings</h2>
      <form onsubmit={save}>
        <label>Name <input bind:value={form.name} maxlength="100" required /></label>
        <label
          >In group
          <select bind:value={form.parent_id}>
            <option value={null}>None (the top level)</option>
            {#each parents as c (c.group.id)}<option value={c.group.id}>{'\u00a0\u00a0'.repeat(c.depth)}{c.group.name}</option>{/each}
          </select></label
        >
        <fieldset>
          <legend>Alerts</legend>
          {#each alerts as a (a.value)}
            <label class="check"><input type="radio" bind:group={form.alert} value={a.value} /> {a.label}</label>
          {/each}
          <p class="muted small">
            A group's alerts are one message when it goes down and one when it is back up, to the group's own channels, on
            top of what its monitors send. Monitors in groups inside it count too; paused ones do not.
          </p>
        </fieldset>
        {#if form.alert === 'selected'}
          <fieldset>
            <legend>Monitors that alert</legend>
            {#each inside as m (m.id)}
              <label class="check"><input type="checkbox" bind:group={form.alert_monitors} value={m.id} /> {m.name}</label>
            {:else}
              <p class="muted">Put monitors in the group first.</p>
            {/each}
          </fieldset>
        {/if}
        {#if form.alert !== 'none'}
          <fieldset>
            <legend>Send alerts to</legend>
            {#each channels as c (c.id)}
              <label class="check"
                ><input type="checkbox" bind:group={form.channels} value={c.id} /> {c.name}{#if !c.enabled}<span class="muted"> (off)</span>{/if}</label
              >
            {:else}
              <p class="muted">No notification channels yet: add one in <a href="/settings/notifications" onclick={link}>Settings → Notifications</a>.</p>
            {/each}
          </fieldset>
        {/if}
        <div class="row"><button class="primary" disabled={busy}>Save</button></div>
      </form>
    </section>
  {/if}
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    gap: 1rem;
    align-items: flex-start;
  }
  .head h1 {
    margin-bottom: 0.25rem;
  }
  .members {
    list-style: none;
    margin: 0;
    padding: 0;
    display: grid;
    gap: 0.4rem;
  }
  .members li {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
  }
  .msg {
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    display: grid;
    gap: 0.4rem;
  }
  .small {
    font-size: 0.85rem;
    margin: 0;
  }
</style>
