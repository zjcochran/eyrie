<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { type AnalysisSettings, type AnalysisSettingsChange, type Channel, message } from '../lib/api.ts';
  import { api } from '../lib/session.svelte.ts';

  let saved = $state<AnalysisSettings | null>(null);
  let form = $state<AnalysisSettingsChange>({
    enabled: false,
    provider: 'api',
    model: '',
    base_url: '',
    timeout_s: 60,
    api_key: '',
    clear_api_key: false,
  });
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let test = $state<{ ok: boolean; message: string } | null>(null);
  // The channels the test alert can go to; 0 only shows the answer here.
  let channels = $state<Channel[]>([]);
  let testChannel = $state(0);
  let busy = $state(false);
  // The provider's models; null until listed. modelsError says why not.
  let models = $state<string[] | null>(null);
  let modelsError = $state('');
  let listing = 0;

  async function loadModels() {
    const n = ++listing;
    modelsError = '';
    try {
      const r = await api.analysisModels({ ...form, timeout_s: Number(form.timeout_s) });
      if (n !== listing) return;
      models = r.error ? null : r.models;
      modelsError = r.error ?? '';
    } catch (e) {
      if (n !== listing) return;
      models = null;
      modelsError = message(e);
    }
  }

  // The saved model stays choosable even if the server no longer lists it.
  let choices = $derived(models && form.model && !models.includes(form.model) ? [form.model, ...models] : models);

  function fill(s: AnalysisSettings) {
    saved = s;
    form = {
      enabled: s.enabled,
      provider: s.provider,
      model: s.model,
      base_url: s.base_url,
      timeout_s: s.timeout_s,
      api_key: '',
      clear_api_key: false,
    };
  }

  onMount(() => {
    api.analysisSettings().then(
      (s) => {
        fill(s);
        void loadModels();
      },
      (e) => (notice = { ok: false, text: message(e) }),
    );
    api.channels().then(
      (cs) => {
        channels = cs;
        testChannel = (cs.find((c) => c.enabled && c.default) ?? cs.find((c) => c.enabled))?.id ?? 0;
      },
      () => {},
    );
  });

  let hasKey = $derived(saved ? (form.provider === 'api' ? saved.has_api_key : saved.has_local_api_key) : false);
  let needsKey = $derived(form.provider !== 'claude-code');

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
      test = await api.testAnalysis({ ...form, timeout_s: Number(form.timeout_s), channel_id: testChannel });
      // The test runs whether analysis is on or not; down alerts do not.
      if (test.ok && !form.enabled) {
        test.message += '\n\nAnalyse down alerts is not ticked: down alerts get no analysis until you tick it and save.';
      } else if (test.ok && !saved?.enabled) {
        test.message += '\n\nSave to turn analysis on for down alerts.';
      }
    } catch (e) {
      test = { ok: false, message: message(e) };
    } finally {
      busy = false;
    }
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

{#if saved}
  {#if !saved.enabled}
    <p class="notice error" role="status">Alert analysis is off: down alerts are sent without an analysis.</p>
  {/if}
  <section class="card">
    <p>
      When a monitor goes down, Eyrie can ask a language model what most likely went wrong, and send the answer in a
      second message after the down alert: Claude with an Anthropic API key, or a model on your own server, such as
      Ollama. Eyrie sends the monitor's name, description and settings (never header values, request bodies or a URL's
      password and query) and its recent checks. The down alert goes out at once and never waits for the model; no
      analysis is sent if the model has not answered within the timeout.
    </p>
    <form onsubmit={save}>
      <label class="check"><input type="checkbox" bind:checked={form.enabled} /> Analyse down alerts</label>
      <fieldset>
        <legend>Analyse with</legend>
        <label class="check"
          ><input type="radio" bind:group={form.provider} value="api" onchange={loadModels} /> Claude, with an Anthropic API key (billed to the
          key's Claude Console account)</label
        >
        {#if saved.claude_code}
          <label class="check"
            ><input type="radio" bind:group={form.provider} value="claude-code" onchange={loadModels} /> Claude, with
            your Claude Pro or Max subscription (through Claude Code)</label
          >
        {/if}
        <label class="check"
          ><input type="radio" bind:group={form.provider} value="local" onchange={loadModels} /> A model on your own server (Ollama, LM Studio,
          llama.cpp, vLLM or another OpenAI-compatible API)</label
        >
      </fieldset>
      {#if form.provider === 'local'}
        <label
          >Server URL
          <input
            type="url"
            bind:value={form.base_url}
            onchange={loadModels}
            required={form.enabled}
            placeholder="http://192.0.2.20:11434/v1"
          /></label
        >
        <p class="muted">
          The server's OpenAI-compatible API, ending in <code>/v1</code>; its models are listed below. Ollama listens on port 11434, LM Studio on
          1234, llama.cpp on 8080 and vLLM on 8000.
        </p>
      {/if}
      {#if form.provider === 'claude-code' && saved.claude_code}
        <p class="muted">
          {#if saved.claude_code.state === 'ready'}
            Claude Code {saved.claude_code.version} is downloaded. It signs in with the
            <code>CLAUDE_CODE_OAUTH_TOKEN</code> in Eyrie's environment (from <code>claude setup-token</code>).
          {:else if saved.claude_code.state === 'failed'}
            Claude Code could not be downloaded: {saved.claude_code.error} Eyrie tries again every hour.
          {:else}
            Claude Code is downloading from Anthropic (about 300 MB); reload this page in a minute.
          {/if}
        </p>
      {/if}
      {#if needsKey}<label
        >{form.provider === 'api' ? 'API key' : 'API key (if the server needs one)'}
        <input
          type="password"
          bind:value={form.api_key}
          onchange={loadModels}
          autocomplete="off"
          placeholder={hasKey ? 'Saved; leave blank to keep it' : form.provider === 'api' ? 'sk-ant-api03-…' : ''}
        /></label
      >{/if}
      {#if needsKey && hasKey}<label class="check"
          ><input type="checkbox" bind:checked={form.clear_api_key} /> Remove the saved API key</label
        >{/if}
      {#if choices}
        <label
          >Model
          <select bind:value={form.model} required={form.enabled && form.provider === 'local'}>
            {#if form.provider === 'api'}<option value="">Default ({saved.default_model})</option>
            {:else if form.provider === 'claude-code'}<option value="">Your account's default</option>
            {:else if !form.model}<option value="" disabled>Choose a model</option>{/if}
            {#each choices as id (id)}<option value={id}>{id}</option>{/each}
          </select></label
        >
      {:else}
        <label
          >Model name
          <input
            bind:value={form.model}
            maxlength="100"
            required={form.enabled && form.provider === 'local'}
            placeholder={form.provider === 'api' ? saved.default_model : 'llama3.2'}
          /></label
        >
      {/if}
      <p class="muted">
        {#if choices}{choices.length} models listed by {form.provider === 'local' ? 'the server' : form.provider === 'api' ? 'the Claude API' : 'Claude Code'}.
        {:else if modelsError}Cannot list the models: {modelsError}
        {:else}Listing the models…{/if}
        <button type="button" onclick={loadModels}>List again</button>
      </p>
      <label>Timeout (seconds) <input type="number" min="5" max="300" bind:value={form.timeout_s} /></label>
      <label
        >Send the test alert to
        <select bind:value={testChannel}>
          <option value={0}>Nowhere: only show the answer here</option>
          {#each channels as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
        </select></label
      >
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
