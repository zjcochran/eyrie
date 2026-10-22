<script lang="ts">
  import { onMount } from 'svelte';
  import Avatar from '../components/Avatar.svelte';
  import { type User, message } from '../lib/api.ts';
  import { api, app } from '../lib/session.svelte.ts';

  let users = $state<User[]>([]);
  let notice = $state<{ ok: boolean; text: string } | null>(null);
  let busy = $state(false);
  let username = $state('');
  let password = $state('');
  let role = $state('viewer');
  let resetFor = $state<User | null>(null);
  let newPassword = $state('');

  const me = $derived(app.session?.user?.id);

  async function load() {
    users = await api.users();
  }

  onMount(() => {
    load().catch((e) => (notice = { ok: false, text: message(e) }));
  });

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

  function add(event: SubmitEvent) {
    event.preventDefault();
    run(async () => {
      const u = await api.createUser(username, password, role);
      username = password = '';
      return `${u.username} added.`;
    });
  }

  function signIn(u: User): string {
    const ways = [];
    if (u.password) ways.push(u.two_factor ? 'password + code' : 'password');
    if (u.sso) ways.push('single sign-on');
    return ways.join(', ');
  }

  function reset(event: SubmitEvent) {
    event.preventDefault();
    const u = resetFor!;
    run(async () => {
      await api.resetPassword(u.id, newPassword);
      resetFor = null;
      newPassword = '';
      return `Password of ${u.username} changed; they were signed out.`;
    });
  }

  function remove(u: User) {
    if (confirm(`Delete ${u.username}? This cannot be undone.`)) {
      run(async () => (await api.deleteUser(u.id), `${u.username} deleted.`));
    }
  }
</script>

{#if notice}<p class="notice" class:ok={notice.ok} class:error={!notice.ok} role="status">{notice.text}</p>{/if}

<section class="card">
  <div class="table-wrap">
    <table>
      <thead><tr><th>User</th><th>Role</th><th>Signs in with</th><th>Last sign-in</th><th></th></tr></thead>
      <tbody>
        {#each users as u (u.id)}
          <tr>
            <td>
              <div class="row"><Avatar user={u} />{u.username}{#if u.disabled}<span class="muted">(disabled)</span>{/if}</div>
            </td>
            <td>
              {#if u.id === me}
                {u.role}
              {:else if u.role_from_group}
                {u.role} <span class="muted">(from the SSO admin group)</span>
              {:else}
                <select
                  value={u.role}
                  disabled={busy}
                  onchange={(e) => {
                    const r = e.currentTarget.value;
                    run(async () => (await api.updateUser(u.id, { role: r }), `${u.username} is now ${r === 'admin' ? 'an admin' : 'a viewer'}.`));
                  }}
                >
                  <option value="viewer">viewer</option>
                  <option value="admin">admin</option>
                </select>
              {/if}
            </td>
            <td>{signIn(u)}</td>
            <td>{u.last_login ? new Date(u.last_login).toLocaleString() : 'never'}</td>
            <td>
              {#if u.id !== me}
                <div class="row">
                  {#if u.disabled}
                    <button disabled={busy} onclick={() => run(async () => (await api.updateUser(u.id, { disabled: false }), `${u.username} enabled.`))}>Enable</button>
                  {:else}
                    <button disabled={busy} onclick={() => run(async () => (await api.updateUser(u.id, { disabled: true }), `${u.username} disabled and signed out.`))}>Disable</button>
                  {/if}
                  {#if u.password}<button disabled={busy} onclick={() => ((resetFor = u), (newPassword = ''))}>Reset password</button>{/if}
                  {#if u.two_factor}
                    <button disabled={busy} onclick={() => run(async () => (await api.resetTwoFactor(u.id), `Two-factor sign-in of ${u.username} is off.`))}>Turn off 2FA</button>
                  {/if}
                  <button class="danger" disabled={busy} onclick={() => remove(u)}>Delete</button>
                </div>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

{#if resetFor}
  <section class="card">
    <h2>New password for {resetFor.username}</h2>
    <form onsubmit={reset}>
      <label>Password <input type="password" bind:value={newPassword} autocomplete="new-password" minlength="12" required /></label>
      <div class="row">
        <button class="primary" disabled={busy}>Set password</button>
        <button type="button" onclick={() => (resetFor = null)}>Cancel</button>
      </div>
    </form>
  </section>
{/if}

<section class="card">
  <h2>Add a user</h2>
  <form onsubmit={add}>
    <label>Username <input bind:value={username} autocomplete="off" required /></label>
    <label>Password <input type="password" bind:value={password} autocomplete="new-password" minlength="12" required /></label>
    <label
      >Role
      <select bind:value={role}>
        <option value="viewer">viewer</option>
        <option value="admin">admin</option>
      </select>
    </label>
    <div><button class="primary" disabled={busy}>Add</button></div>
  </form>
</section>
