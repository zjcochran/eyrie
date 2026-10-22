<script lang="ts">
  import { app } from '../lib/session.svelte.ts';
</script>

<h1>Monitors</h1>
<div class="card">
  <p>Welcome, {app.session?.user?.username}. There are no monitors yet: adding them comes with the next version of Eyrie.</p>
</div>
