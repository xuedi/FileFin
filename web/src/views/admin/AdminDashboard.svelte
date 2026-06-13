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
      <span class="ff-dash-num">{s.enrich.pending}</span>
      <span class="ff-dash-label">Enrich queued</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.imports.active}</span>
      <span class="ff-dash-label">Imports running</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.health.issues}</span>
      <span class="ff-dash-label">Health issues - {s.health.unchecked} unchecked (discovery {s.health.discovery})</span>
    </div>
  </div>
  {#if app.health && app.health.items.length}
    <h2 class="title is-5 ff-health-head">Health issues</h2>
    <table class="table is-fullwidth">
      <thead>
        <tr><th>Title</th><th>Issues</th><th>Last checked</th></tr>
      </thead>
      <tbody>
        {#each app.health.items as it}
          <tr>
            <td>{it.title || it.id}</td>
            <td>
              {#each it.issues as iss}
                <span class="tag is-warning ff-health-tag" title={iss.detail}>{iss.code}</span>
              {/each}
            </td>
            <td class="has-text-grey">{it.lastChecked}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
{:else}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{/if}
