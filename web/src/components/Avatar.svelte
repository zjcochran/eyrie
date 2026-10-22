<script lang="ts">
  import type { User } from '../lib/api.ts';

  let { user, size = 28 }: { user: User; size?: number } = $props();
</script>

{#if user.picture}
  <img src={user.picture} alt="" width={size} height={size} style:width="{size}px" style:height="{size}px" />
{:else}
  <span class="initial" aria-hidden="true" style:width="{size}px" style:height="{size}px" style:font-size="{size / 2}px"
    >{user.username.slice(0, 1).toUpperCase()}</span
  >
{/if}

<style>
  img,
  .initial {
    border-radius: 50%;
    flex: none;
    object-fit: cover;
  }
  .initial {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: var(--accent);
    color: var(--accent-fg);
    font-weight: 600;
  }
</style>
