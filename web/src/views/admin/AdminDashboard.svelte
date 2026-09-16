<script>
  import { getContext } from 'svelte'
  const app = getContext('app')
</script>

<div class="ff-page-head">
  <h1 class="title is-4">Dashboard</h1>
  <div class="buttons">
    <button class="button" disabled={app.discoveryRunning} onclick={() => app.runDiscovery()}>
      {app.discoveryRunning ? 'Starting...' : 'Run discovery now'}
    </button>
  </div>
</div>
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
      <span class="ff-dash-num">{s.optimizer.coverage}%</span>
      <span class="ff-dash-label">Optimized ({s.optimizer.optimized}/{s.optimizer.needsCopy} need a copy)</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.enrich.pending}</span>
      <span class="ff-dash-label">Enrich queued</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.imports.active}</span>
      <span class="ff-dash-label">Imports running</span>
    </div>
    <button class="box ff-dash-card ff-dash-link" onclick={() => app.go('/admin/attention')}>
      <span class="ff-dash-num">{s.attention.total}</span>
      <span class="ff-dash-label">Needs attention - {s.health.unchecked} unchecked (discovery {s.health.discovery})</span>
    </button>
  </div>
{:else}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{/if}
