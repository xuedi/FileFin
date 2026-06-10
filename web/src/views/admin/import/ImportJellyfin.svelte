<script>
  import { getContext } from 'svelte'
  import { treeMarker } from '../../../lib/app.svelte.js'
  import FolderBrowser from '../../../components/FolderBrowser.svelte'
  const app = getContext('app')
</script>

<p class="label is-small has-text-grey">Jellyfin library folder (on the server)</p>
<div class="ff-import-row">
  <input class="input ff-grow" bind:value={app.jellyfinDir} placeholder="path to the NFO library directory" />
  <button type="button" class="button" onclick={() => app.openJellyfinBrowse()}>Browse</button>
</div>
{#if app.jellyfinError}<p class="has-text-danger">{app.jellyfinError}</p>{/if}

{#if app.jellyfinBrowseOpen}
  <FolderBrowser
    title="Select the Jellyfin library folder"
    path={app.jellyfinBrowse.path}
    parent={app.jellyfinBrowse.parent}
    error={app.jellyfinBrowseError}
    entries={app.jellyfinBrowse.entries}
    onUp={() => app.jellyfinBrowseNavigate(app.jellyfinBrowse.parent)}
    onEntry={(e) => app.jellyfinBrowseNavigate(e.path)}
    onClose={() => (app.jellyfinBrowseOpen = false)}
    entryLabel={(e) => e.name + '/'}
    onSelect={() => app.selectJellyfinDir()} />
{/if}

{#if app.jellyfinStaging || app.jellyfinProgress}
  <p class="label is-small has-text-grey">Loading media files</p>
  {@const total = app.jellyfinProgress?.total || 0}
  {@const done = app.jellyfinProgress?.done || 0}
  <progress class="progress is-primary" value={total ? Math.round((done / total) * 100) : 0} max="100"></progress>
  <p class="has-text-grey has-text-centered">{done} / {total} files - {app.jellyfinProgress?.staged || 0} staged, {app.jellyfinProgress?.missing || 0} missing</p>
{:else}
  <p class="label is-small has-text-grey">Target category</p>
  <div class="ff-import-row">
    <div class="select">
      <select bind:value={app.jellyfinCategoryId}>
        <option value={0}>Create a new category</option>
        {#each app.categoryTree as c}
          <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
        {/each}
      </select>
    </div>
    {#if app.jellyfinCategoryId === 0}
      <input class="input" placeholder="New category folder name" bind:value={app.jellyfinNewName} />
    {/if}
  </div>
  <button class="button is-primary" disabled={!app.jellyfinReady} onclick={() => app.startJellyfinStaging()}>Load media files</button>
{/if}
