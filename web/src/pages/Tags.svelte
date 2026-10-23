<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import TagChip from '../components/TagChip.svelte';
  import { type Tag, message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  let tags = $state<Tag[]>([]);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);
  // The tag being edited: null for none; id 0 for a new one.
  let editing = $state<Tag | null>(null);
  let name = $state('');
  let color = $state('#1f5fbf');

  async function load() {
    tags = await api.tags();
  }

  onMount(() => {
    load().catch((e) => (notice = { ok: false, text: message(e) }));
  });

  function edit(t: Tag | null) {
    editing = t ?? ({ id: 0 } as Tag);
    name = t?.name ?? '';
    color = t?.color ?? '#1f5fbf';
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
    run(async () => {
      const t = id ? await api.updateTag(id, name, color) : await api.createTag(name, color);
      editing = null;
      return `${t.name} saved.`;
    });
  }

  function remove(t: Tag) {
    if (confirm(`Delete the tag ${t.name}? It comes off ${t.monitors} monitors and any maintenance windows.`)) {
      run(async () => (await api.deleteTag(t.id), `${t.name} deleted.`));
    }
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

<section class="card">
  <div class="row between">
    <h2>Tags</h2>
    <button class="primary" onclick={() => edit(null)} disabled={busy}>Add a tag</button>
  </div>
  <p class="muted">
    Tags label monitors, each with an optional value (such as <em>env: prod</em>). Give them to monitors in the monitor's
    settings; filter the monitor list by them, and cover the monitors with a tag by a maintenance window.
  </p>
  {#if tags.length === 0}
    <p class="muted">No tags yet.</p>
  {:else}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Tag</th><th>Monitors</th><th></th></tr></thead>
        <tbody>
          {#each tags as t (t.id)}
            <tr>
              <td><TagChip name={t.name} color={t.color} /></td>
              <td>{t.monitors}</td>
              <td>
                <div class="row">
                  <button disabled={busy} onclick={() => edit(t)}>Edit</button>
                  <button class="danger" disabled={busy} onclick={() => remove(t)}>Delete</button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

{#if editing}
  <section class="card">
    <h2>{editing.id ? `Edit ${editing.name}` : 'Add a tag'}</h2>
    <form onsubmit={save}>
      <label>Name <input bind:value={name} maxlength="50" required placeholder="prod" /></label>
      <label class="color">Colour <input type="color" bind:value={color} /> <TagChip name={name || 'tag'} {color} /></label>
      <div class="row">
        <button class="primary" disabled={busy}>Save</button>
        <button type="button" onclick={() => (editing = null)}>Cancel</button>
      </div>
    </form>
  </section>
{/if}

<style>
  .between {
    justify-content: space-between;
  }
  .color input {
    width: 3rem;
    padding: 0.1rem;
    height: 2rem;
  }
</style>
