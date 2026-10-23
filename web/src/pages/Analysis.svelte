<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type AnalysisSettings, type AnalysisSettingsChange, message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  let saved = $state<AnalysisSettings | null>(null);
  let form = $state<AnalysisSettingsChange>({
    enabled: false,
    provider: 'api',
    model: '',
    timeout_s: 60,
    api_key: '',
    clear_api_key: false,
  });
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let test = $state<{ ok: boolean; message: string } | null>(null);
  let busy = $state(false);

  function fill(s: AnalysisSettings) {
    saved = s;
    form = {
      enabled: s.enabled,
      provider: s.provider,
      model: s.model,
      timeout_s: s.timeout_s,
      api_key: '',
      clear_api_key: false,
    };
  }

  onMount(() => {
    api.analysisSettings().then(fill, (e) => (notice = { ok: false, text: message(e) }));
  });

  async function save(event: SubmitEvent) {
    event.preventDefault();
    busy = true;
    notice = null;
    try {
      fill(await api.saveAnalysisSettings({ ...form, timeout_s: Number(form.timeout_s) }));
      notice = { ok: true, text: 'Alert analysis settings saved.' };
    } catch (e) {
      notice = { ok: false, text: message(e) };
    } finally {
      busy = false;
    }
  }

  async function runTest() {
    busy = true;
    test = null;
    try {
      test = await api.testAnalysis({ ...form, timeout_s: Number(form.timeout_s) });
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
      When a monitor goes down, Eyrie can ask Claude what most likely went wrong, and put the answer in the alert. Eyrie
      sends Claude the monitor's name, description and settings (never header values, request bodies or a URL's
      password and query) and its recent checks. Alerts wait at most the timeout, and go out without an analysis if
      Claude has not answered by then.
    </p>
    <form onsubmit={save}>
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> Analyse down alerts with Claude</label>
      <fieldset>
        <legend>Connect with</legend>
        <label class="check"
          ><input type="radio" bind:group={form.provider} value="api" /> An Anthropic API key (billed to the key's Claude
          Console account)</label
        >
        <label class="check"
          ><input type="radio" bind:group={form.provider} value="claude-code" /> Claude Code, signed in with your Claude subscription</label
        >
      </fieldset>
      {#if form.provider === 'api'}
        <label
          >API key
          <input
            type="password"
            bind:value={form.api_key}
            autocomplete="off"
            placeholder={saved.has_api_key ? 'Saved; leave blank to keep it' : 'sk-ant-api03-…'}
          /></label
        >
        {#if saved.has_api_key}<label class="check"
            ><input type="checkbox" bind:checked={form.clear_api_key} /> Remove the saved API key</label
          >{/if}
      {:else}
        <p class="muted">
          {#if saved.claude_code}
            Claude Code found at <code>{saved.claude_code}</code>. Sign it in as the user Eyrie runs as, with
            <code>claude auth login</code> or a <code>CLAUDE_CODE_OAUTH_TOKEN</code> from <code>claude setup-token</code>;
            Eyrie never sees your sign-in.
          {:else}
            {saved.claude_code_error} Install Claude Code where Eyrie runs, or point <code>EYRIE_CLAUDE_COMMAND</code> at
            it.
          {/if}
        </p>
      {/if}
      <label
        >Model
        <input
          bind:value={form.model}
          maxlength="100"
          placeholder={form.provider === 'api' ? saved.default_model : 'Your account’s default'}
        /></label
      >
      <label>Timeout (seconds) <input type="number" min="5" max="300" bind:value={form.timeout_s} /></label>
      <div class="row">
        <button type="button" onclick={runTest} disabled={busy}>Test with a made-up alert</button>
        <button class="primary" disabled={busy}>Save</button>
      </div>
      {#if test}<p class="answer" class:ok={test.ok} class:bad={!test.ok}>{test.ok ? '✓' : '✗'} {test.message}</p>{/if}
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
  .answer {
    white-space: pre-wrap;
  }
  fieldset {
    border: none;
    padding: 0;
    margin: 0;
  }
</style>
