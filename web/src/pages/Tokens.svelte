<script lang="ts">
  import { onMount } from 'svelte';
  import { type Token, message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  let tokens = $state<Token[]>([]);
  let name = $state('');
  let created = $state<{ name: string; token: string } | null>(null);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);

  async function load() {
    tokens = await api.tokens();
  }

  onMount(() => {
    load().catch((e) => (notice = { ok: false, text: message(e) }));
  });

  async function run(f: () => Promise<void>) {
    busy = true;
    notice = null;
    try {
      await f();
      await load();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  function create(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      const t = await api.createToken(name);
      created = { name: t.name, token: t.token };
      name = '';
    });
  }

  function revoke(t: Token) {
    if (confirm(`Revoke ${t.name}? Anything using it stops working at once.`)) {
      run(async () => {
        await api.deleteToken(t.id);
        notice = { ok: true, text: `${t.name} revoked.` };
      });
    }
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

{#if created}
  <section class="card">
    <h2>{created.name}</h2>
    <p>Copy the token now: it is not shown again.</p>
    <p class="mono">{created.token}</p>
    <button onclick={() => (created = null)}>Done</button>
  </section>
{/if}

<section class="card">
  <p>
    Tokens let scripts read Eyrie with <code>Authorization: Bearer &lt;token&gt;</code>. A token can only read; it cannot
    sign in or change anything.
  </p>
  {#if tokens.length}
    <div class="table-wrap">
      <table>
        <thead><tr><th>Name</th><th>Created</th><th>Last used</th><th></th></tr></thead>
        <tbody>
          {#each tokens as t (t.id)}
            <tr>
              <td>{t.name}</td>
              <td>{new Date(t.created_at).toLocaleString()} by {t.created_by}</td>
              <td>{t.last_used ? new Date(t.last_used).toLocaleString() : 'never'}</td>
              <td><button class="danger" disabled={busy} onclick={() => revoke(t)}>Revoke</button></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="muted">No tokens yet.</p>
  {/if}
</section>

<section class="card">
  <h2>New token</h2>
  <form onsubmit={create}>
    <label>Name <input bind:value={name} maxlength="64" placeholder="ci" required /></label>
    <div><button class="primary" disabled={busy}>Create</button></div>
  </form>
</section>
