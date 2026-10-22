<script lang="ts">
  import { onMount } from 'svelte';
  import { type SSOSettings, message } from '../lib/api.ts';
  import { api, refresh } from '../lib/session.svelte.ts';

  let saved = $state<SSOSettings | null>(null);
  let form = $state({
    enabled: false,
    issuer: '',
    client_id: '',
    client_secret: '',
    clear_secret: false,
    display_name: '',
    auto_provision: true,
    allowed_group: '',
    admin_group: '',
    sso_only: false,
  });
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let test = $state<{ ok: boolean; message: string } | null>(null);
  let busy = $state(false);

  function fill(s: SSOSettings) {
    saved = s;
    form = { ...form, ...s, client_secret: '', clear_secret: false };
  }

  onMount(() => {
    api.ssoSettings().then(fill, (e) => (notice = { ok: false, text: message(e) }));
  });

  async function save(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    notice = null;
    try {
      fill(await api.saveSSOSettings(form));
      notice = { ok: true, text: 'Single sign-on settings saved.' };
      await refresh();
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  async function runTest() {
    busy = true;
    try {
      test = await api.testSSO(form.issuer);
    } catch (e) {
      test = { ok: false, message: message(e) };
    } finally {
      busy = false;
    }
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

{#if saved}
  <section class="card">
    <p>
      Sign in with an OpenID Connect provider. Register Eyrie there with this callback URL:
      <code>{saved.callback_url}</code>
    </p>
    <form onsubmit={save}>
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> Turn on single sign-on</label>
      <label>Issuer URL <input bind:value={form.issuer} placeholder="https://id.example.com" /></label>
      <div class="row">
        <button type="button" onclick={runTest} disabled={busy || !form.issuer}>Test connection</button>
        {#if test}<span class:ok={test.ok} class:bad={!test.ok}>{test.ok ? '✓' : '✗'} {test.message}</span>{/if}
      </div>
      <label>Client ID <input bind:value={form.client_id} /></label>
      <label
        >Client secret
        <input
          type="password"
          bind:value={form.client_secret}
          autocomplete="off"
          placeholder={saved.has_secret ? 'Saved; leave blank to keep it' : ''}
        /></label
      >
      {#if saved.has_secret}<label class="check"
          ><input type="checkbox" bind:checked={form.clear_secret} /> Remove the saved client secret</label
        >{/if}
      <label>Button label <input bind:value={form.display_name} placeholder="SSO" /></label>
      <label>Allowed group <input bind:value={form.allowed_group} placeholder="Anyone at the provider" /></label>
      <label>Admin group <input bind:value={form.admin_group} /></label>
      <label class="check"
        ><input type="checkbox" bind:checked={form.auto_provision} /> Create an account the first time someone signs in</label
      >
      <label class="check"
        ><input type="checkbox" bind:checked={form.sso_only} /> Turn off password sign-in (your own account must be linked)</label
      >
      <div><button class="primary" disabled={busy}>Save</button></div>
    </form>
  </section>
{/if}

<style>
  .ok {
    color: var(--ok);
  }
  .bad {
    color: var(--error);
  }
</style>
