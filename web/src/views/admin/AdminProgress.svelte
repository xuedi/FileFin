<script>
  import { getContext } from 'svelte'
  import { pct } from '../../lib/app.svelte.js'
  import ProgressBar from '../../components/ProgressBar.svelte'
  const app = getContext('app')
</script>

<h1 class="title is-4">Progress</h1>

<h2 class="title is-6 ff-prog-section">Imports</h2>
{#if app.importActive.length === 0}
  <p class="has-text-grey">No imports running.</p>
{:else}
  <table class="table is-fullwidth">
    <thead><tr><th>Category</th><th>Title</th><th>Progress</th></tr></thead>
    <tbody>
      {#each app.importActive as row}
        <tr>
          <td>{row.category}</td>
          <td>{row.title || row.filename}</td>
          <td><ProgressBar value={pct(row)} /></td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}
{#if app.importPending > 0}<p class="has-text-grey is-size-7 ff-prog-waiting">{app.importPending} more waiting in line</p>{/if}

<h2 class="title is-6 ff-prog-section">Optimizing</h2>
{#if app.optimizeRows.length === 0}
  <p class="has-text-grey">No encodes running.</p>
{:else}
  <table class="table is-fullwidth">
    <thead><tr><th>Title</th><th>File</th><th>Agent</th><th>Progress</th></tr></thead>
    <tbody>
      {#each app.optimizeRows as row}
        <tr>
          <td>{row.title}</td>
          <td>{row.file}</td>
          <td>{row.agent}</td>
          <td><ProgressBar value={row.percent} /></td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}
{#if app.optimizePending > 0}<p class="has-text-grey is-size-7 ff-prog-waiting">{app.optimizePending} more waiting in line</p>{/if}

<h2 class="title is-6 ff-prog-section">Enriching</h2>
{#if app.enrichRows.length === 0}
  <p class="has-text-grey">No enrichment running.</p>
{:else}
  <table class="table is-fullwidth">
    <thead><tr><th>Title</th><th>Agent</th><th>Status</th></tr></thead>
    <tbody>
      {#each app.enrichRows as row}
        <tr><td>{row.title}</td><td>{row.agent}</td><td>looking up...</td></tr>
      {/each}
    </tbody>
  </table>
{/if}
{#if app.enrichPending > 0}<p class="has-text-grey is-size-7 ff-prog-waiting">{app.enrichPending} more waiting in line</p>{/if}

<h2 class="title is-6 ff-prog-section">Thumbnails</h2>
{#if app.thumbnailRows.length === 0}
  <p class="has-text-grey">No thumbnails running.</p>
{:else}
  <table class="table is-fullwidth">
    <thead><tr><th>Title</th><th>Agent</th><th>Status</th></tr></thead>
    <tbody>
      {#each app.thumbnailRows as row}
        <tr><td>{row.title}</td><td>{row.agent}</td><td>generating...</td></tr>
      {/each}
    </tbody>
  </table>
{/if}
{#if app.thumbnailPending > 0}<p class="has-text-grey is-size-7 ff-prog-waiting">{app.thumbnailPending} more waiting in line</p>{/if}
