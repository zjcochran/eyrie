<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import {
    type Incident,
    type IncidentStatus,
    type StatusPage,
    type StatusPageChange,
    type Tag,
    message,
  } from '../lib/api.ts';
  import { badgeURLs, incidentLabels, incidentStatuses } from '../lib/incidents.ts';
  import { live } from '../lib/live.svelte.ts';
  import { go, link } from '../lib/nav.svelte.ts';
  import { api, app } from '../lib/session.svelte.ts';

  /** The page to edit; 0 makes a new one. */
  let { id = 0 }: { id?: number } = $props();

  const admin = $derived(app.session?.user?.role === 'admin');
  let saved = $state<StatusPage | null>(null);
  let tags = $state<Tag[]>([]);
  let incidents = $state<Incident[]>([]);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);
  let form = $state<StatusPageChange>({
    slug: '',
    title: '',
    description: '',
    footer: '',
    published: false,
    theme: 'auto',
    accent: '',
    show_tags: false,
    show_cert_expiry: false,
    sections: [{ title: '', tag_id: null, monitors: [] }],
  });
  let customAccent = $state(false);

  // A new incident, and the update being written for each incident.
  let opening = $state({ title: '', status: 'investigating' as IncidentStatus, message: '' });
  let updates = $state<Record<number, { status: IncidentStatus; message: string }>>({});

  function fill(p: StatusPage) {
    saved = p;
    form = {
      slug: p.slug,
      title: p.title,
      description: p.description,
      footer: p.footer,
      published: p.published,
      theme: p.theme,
      accent: p.accent,
      show_tags: p.show_tags,
      show_cert_expiry: p.show_cert_expiry,
      sections: p.sections.map((s) => ({ ...s, monitors: [...s.monitors] })),
    };
    customAccent = p.accent !== '';
  }

  async function loadIncidents() {
    incidents = await api.incidents(id);
    updates = Object.fromEntries(incidents.map((i) => [i.id, { status: i.status, message: '' }]));
  }

  onMount(async () => {
    try {
      tags = await api.tags();
      if (id) {
        fill(await api.statusPage(id));
        await loadIncidents();
      }
    } catch (e) {
      notice = { ok: false, text: message(e) };
    }
  });

  async function run(f: () => Promise<string>) {
    busy = true;
    notice = null;
    try {
      notice = { ok: true, text: await f() };
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function save(event: SubmitEvent) {
    event.preventDefault();
    const change = { ...form, accent: customAccent ? form.accent || '#1f5fbf' : '' };
    run(async () => {
      if (!id) {
        const p = await api.createStatusPage(change);
        go(`/status-pages/${p.id}`);
        return `${p.title} added.`;
      }
      fill(await api.updateStatusPage(id, change));
      return 'Saved.';
    });
  }

  function remove() {
    if (saved && confirm(`Delete the status page ${saved.title}, with its incidents? Its address stops working.`)) {
      run(async () => {
        await api.deleteStatusPage(id);
        go('/status-pages');
        return 'Deleted.';
      });
    }
  }

  function move(i: number, by: number) {
    const s = form.sections;
    [s[i], s[i + by]] = [s[i + by], s[i]];
  }

  function uploadLogo(event: Event) {
    const file = (event.target as HTMLInputElement).files?.[0];
    if (file) run(async () => (fill(await api.uploadLogo(id, file)), 'Logo saved.'));
  }

  function openIncident(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      await api.openIncident(id, opening.title, opening.status, opening.message);
      opening = { title: '', status: 'investigating', message: '' };
      await loadIncidents();
      return 'Incident opened.';
    });
  }

  function postUpdate(event: SubmitEvent, i: Incident) {
    event.preventDefault();
    const u = updates[i.id];
    run(async () => {
      await api.updateIncident(i.id, u.status, u.message);
      await loadIncidents();
      return 'Update posted.';
    });
  }

  function rename(i: Incident) {
    const title = prompt('Title of the incident', i.title);
    if (title) run(async () => (await api.renameIncident(i.id, title), await loadIncidents(), 'Renamed.'));
  }

  function deleteIncident(i: Incident) {
    if (confirm(`Delete the incident ${i.title} and its timeline?`)) {
      run(async () => (await api.deleteIncident(i.id), await loadIncidents(), 'Incident deleted.'));
    }
  }

  // The monitors on the page, for their badges.
  const onPage = $derived(
    live.monitors.filter((m) =>
      form.sections.some((s) => s.monitors.includes(m.id) || (s.tag_id !== null && m.tags.some((t) => t.id === s.tag_id))),
    ),
  );
  const when = (iso: string) => new Date(iso).toLocaleString();
</script>

<p><a href="/status-pages" onclick={link}>← Status pages</a></p>
<h1>{id ? (saved?.title ?? '') : 'Add a status page'}</h1>
{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}
{#if saved}<p><a href="/status/{saved.slug}" onclick={link}>Open the page</a>{#if !saved.published} (a preview: not published){/if}</p>{/if}

{#if admin}
  <form class="card" onsubmit={save}>
    <h2>Settings</h2>
    <label>Title <input bind:value={form.title} maxlength="100" required placeholder="Shop status" /></label>
    <label
      >Address <span class="row addr"
        ><span class="muted">/status/</span><input bind:value={form.slug} maxlength="64" required pattern="[a-z0-9]+(-[a-z0-9]+)*" placeholder="shop" /></span
      >
    </label>
    <span class="muted help">Lower-case letters, digits and single dashes.</span>
    <label>Description <textarea bind:value={form.description} maxlength="2000" rows="2"></textarea></label>
    <label>Footer <textarea bind:value={form.footer} maxlength="2000" rows="2"></textarea></label>
    <div class="row">
      <label
        >Theme
        <select bind:value={form.theme}>
          <option value="auto">The visitor's</option>
          <option value="light">Light</option>
          <option value="dark">Dark</option>
        </select>
      </label>
      <label class="check"><input type="checkbox" bind:checked={customAccent} /> Accent colour</label>
      {#if customAccent}<input type="color" bind:value={form.accent} aria-label="Accent colour" />{/if}
    </div>
    <label class="check"><input type="checkbox" bind:checked={form.show_tags} /> Show the monitors' tags</label>
    <label class="check"><input type="checkbox" bind:checked={form.show_cert_expiry} /> Show certificate expiry</label>
    <label class="check"><input type="checkbox" bind:checked={form.published} /> Published: anyone can see it</label>

    <h3>Sections</h3>
    <p class="muted">Each section shows the monitors with its tag and the monitors ticked, by name.</p>
    {#each form.sections as sec, i (i)}
      <fieldset>
        <legend>Section {i + 1}</legend>
        <label>Title <input bind:value={sec.title} maxlength="100" placeholder="Website" /></label>
        <label
          >Monitors with the tag
          <select bind:value={sec.tag_id}>
            <option value={null}>None</option>
            {#each tags as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
          </select>
        </label>
        <div class="monitors">
          {#each live.monitors as m (m.id)}
            <label class="check"><input type="checkbox" bind:group={sec.monitors} value={m.id} /> {m.name}</label>
          {/each}
        </div>
        <div class="row">
          <button type="button" disabled={i === 0} onclick={() => move(i, -1)}>Up</button>
          <button type="button" disabled={i === form.sections.length - 1} onclick={() => move(i, 1)}>Down</button>
          <button type="button" class="danger" onclick={() => form.sections.splice(i, 1)}>Remove</button>
        </div>
      </fieldset>
    {/each}
    <div>
      <button type="button" onclick={() => form.sections.push({ title: '', tag_id: null, monitors: [] })}>Add a section</button>
    </div>

    <div class="row">
      <button class="primary" disabled={busy}>{id ? 'Save' : 'Add'}</button>
      {#if id}<button type="button" class="danger" disabled={busy} onclick={remove}>Delete the page</button>{/if}
    </div>
  </form>

  {#if saved}
    <section class="card">
      <h2>Logo</h2>
      {#if saved.logo_url}<img class="logo" src={saved.logo_url} alt="The page's logo" />{/if}
      <div class="row">
        <label class="button">Upload <input type="file" accept="image/png,image/jpeg,image/gif,image/webp" onchange={uploadLogo} hidden /></label>
        {#if saved.logo_url}<button disabled={busy} onclick={() => run(async () => (fill(await api.removeLogo(id)), 'Logo removed.'))}>Remove</button>{/if}
      </div>
    </section>
  {/if}
{/if}

{#if saved}
  <section class="card">
    <h2>Incidents</h2>
    {#if admin}
      <form onsubmit={openIncident}>
        <label>Title <input bind:value={opening.title} maxlength="200" required placeholder="Checkout errors" /></label>
        <label
          >Status
          <select bind:value={opening.status}>
            {#each incidentStatuses as s (s)}<option value={s}>{incidentLabels[s]}</option>{/each}
          </select>
        </label>
        <label>What happened <textarea bind:value={opening.message} maxlength="5000" rows="3" required></textarea></label>
        <div><button class="primary" disabled={busy}>Open an incident</button></div>
      </form>
    {/if}
    {#each incidents as i (i.id)}
      <article class="incident">
        <h3>{i.title} <span class="muted">· {incidentLabels[i.status]}</span></h3>
        <ol>
          {#each i.updates as u, n (n)}
            <li>
              <strong>{incidentLabels[u.status]}</strong>
              <span class="muted">· {when(u.created_at)}{u.author ? ` · ${u.author}` : ''}</span>
              <p class="text">{u.message}</p>
            </li>
          {/each}
        </ol>
        {#if admin && updates[i.id]}
          <form onsubmit={(e) => postUpdate(e, i)}>
            <div class="row">
              <select bind:value={updates[i.id].status} aria-label="Status">
                {#each incidentStatuses as s (s)}<option value={s}>{incidentLabels[s]}</option>{/each}
              </select>
              <input class="grow" bind:value={updates[i.id].message} maxlength="5000" required placeholder="An update" />
              <button disabled={busy}>Post</button>
            </div>
          </form>
          <div class="row">
            <button disabled={busy} onclick={() => rename(i)}>Rename</button>
            <button class="danger" disabled={busy} onclick={() => deleteIncident(i)}>Delete</button>
          </div>
        {/if}
      </article>
    {:else}
      <p class="muted">No incidents.</p>
    {/each}
  </section>

  <section class="card">
    <h2>Badges</h2>
    <p class="muted">
      SVG images of the page's monitors, for README files and dashboards. They work only while the page is published.
      Add <code>?label=</code> to change the text on the left.
    </p>
    {#each onPage as m (m.id)}
      <h3>{m.name}</h3>
      {#each badgeURLs(location.origin, m.id) as b (b.url)}
        <div class="row badge">
          {#if saved.published}<img src={b.url} alt={b.label} />{/if}
          <span class="muted">{b.label}</span>
          <code>{b.url}</code>
        </div>
      {/each}
    {:else}
      <p class="muted">No monitors on this page yet.</p>
    {/each}
  </section>
{/if}

<style>
  form.card,
  form {
    display: grid;
    gap: 0.75rem;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    display: grid;
    gap: 0.6rem;
    padding: 0.75rem;
  }
  textarea {
    font: inherit;
    padding: 0.45rem 0.6rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--fg);
  }
  .addr input {
    flex: 1;
  }
  .help {
    margin-top: -0.5rem;
    font-size: 0.85rem;
  }
  .monitors {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
    gap: 0.2rem 0.9rem;
    max-height: 12rem;
    overflow-y: auto;
  }
  .logo {
    max-height: 4rem;
    max-width: 12rem;
  }
  .incident {
    border-top: 1px solid var(--border);
    padding-top: 0.5rem;
    margin-top: 0.75rem;
  }
  .incident ol {
    list-style: none;
    padding: 0 0 0 0.8rem;
    border-left: 2px solid var(--border);
  }
  .text {
    white-space: pre-wrap;
    margin: 0.2rem 0 0.5rem;
  }
  .grow {
    flex: 1;
    min-width: 0;
  }
  .badge {
    flex-wrap: wrap;
    gap: 0.6rem;
    margin-bottom: 0.3rem;
  }
  .badge code {
    font-size: 0.8rem;
    overflow-wrap: anywhere;
  }
</style>
