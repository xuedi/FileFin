<script>
  import { getContext } from 'svelte'
  const app = getContext('app')
</script>

<h1 class="title is-4">Dashboard</h1>
{#if app.summary}
  {@const s = app.summary}
  <div class="ff-dash">
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.library.media}</span>
      <span class="ff-dash-label">Media in {s.library.categories} categories</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.library.files}</span>
      <span class="ff-dash-label">Media files</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.users.total}</span>
      <span class="ff-dash-label">Users ({s.users.admins} admin)</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.optimizer.active}</span>
      <span class="ff-dash-label">Optimizing - {s.optimizer.pending} queued ({s.optimizer.mode})</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.enrich.pending}</span>
      <span class="ff-dash-label">Enrich queued</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.imports.active}</span>
      <span class="ff-dash-label">Imports running</span>
    </div>
  </div>
{:else}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{/if}
