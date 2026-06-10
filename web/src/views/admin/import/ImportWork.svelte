<script>
  import { getContext } from 'svelte'
  import ImportPlex from './ImportPlex.svelte'
  import ImportJellyfin from './ImportJellyfin.svelte'
  import ImportUpload from './ImportUpload.svelte'
  import ImportAssess from './ImportAssess.svelte'
  const app = getContext('app')
</script>

<button class="button is-ghost is-small ff-back" onclick={() => app.go('/admin/library')}>&larr; Back</button>
{#if app.importOrigin === 'plex'}
  <h1 class="title is-4">Import from Plex</h1>
{:else if app.importOrigin === 'jellyfin'}
  <h1 class="title is-4">Import from Jellyfin</h1>
{:else}
  <h1 class="title is-4">Import into {app.categoryAlias(app.importCategory)}</h1>
{/if}

{#if app.importPage === 'plex'}
  <ImportPlex />
{:else if app.importPage === 'jellyfin'}
  <ImportJellyfin />
{:else if app.importPage === 'upload'}
  <ImportUpload />
{:else}
  <ImportAssess />
{/if}
