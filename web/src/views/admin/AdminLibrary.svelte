<script>
  import { getContext } from 'svelte'
  import { treeMarker } from '../../lib/app.svelte.js'
  const app = getContext('app')
</script>

<h1 class="title is-4">Library</h1>
{#if app.catError}<p class="has-text-danger">{app.catError}</p>{/if}
<table class="table is-fullwidth">
  <thead>
    <tr>
      <th>Folder</th>
      <th>Alias</th>
      <th title="Other media (home videos / recordings): skips OMDb lookups and derives posters from a video frame instead.">Other media</th>
      <th></th>
    </tr>
  </thead>
  <tbody>
    {#each app.categoryTree as c}
      <tr>
        <td><span class="ff-cat-tree">{treeMarker(c._depth)}</span>{c.leaf ?? c.name}</td>
        <td>
          {#if app.editName === c.name}
            <input class="input is-small ff-inline-input" bind:value={app.editAlias} onkeydown={(e) => e.key === 'Enter' && app.saveAlias()} />
          {:else}
            {c.alias}
          {/if}
        </td>
        <td class="has-text-centered">
          {#if c._depth === 0}
            <input type="checkbox" checked={c.otherMedia} onchange={(e) => app.toggleOtherMedia(c, e.currentTarget.checked)} />
          {:else}
            <span class="has-text-grey is-size-7 is-italic" title="Inherited from the top-level category">inherited</span>
          {/if}
        </td>
        <td class="ff-row-actions">
          {#if app.editName === c.name}
            <button class="button is-small is-primary" onclick={() => app.saveAlias()}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editName = '')}>Cancel</button>
          {:else}
            <button class="button is-small" onclick={() => app.startEditAlias(c)}>Edit</button>
            <button
              class="button is-small is-danger"
              disabled={!c.empty}
              title={c.empty ? 'Delete this empty category' : 'Folder is not empty'}
              onclick={() => app.deleteCategory(c.name)}>Delete</button>
          {/if}
        </td>
      </tr>
    {/each}
    {#if app.categories.length === 0}
      <tr><td colspan="4" class="has-text-grey">No categories yet.</td></tr>
    {/if}
  </tbody>
</table>
<div class="ff-add-row">
  <input class="input" placeholder="Folder name" bind:value={app.catName} />
  <input class="input" placeholder="Alias (defaults to folder name)" bind:value={app.catAlias} />
  <div class="select">
    <select bind:value={app.catParentId}>
      <option value={0}>(top level)</option>
      {#each app.categoryTree as c}
        <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
      {/each}
    </select>
  </div>
  <button class="button is-primary" disabled={!app.catName.trim()} onclick={() => app.addCategory()}>Add</button>
</div>

<h1 class="title is-4 ff-import-head">Import</h1>
{#if app.mediaFormat === ''}
  <p class="has-text-grey">
    To import media, please select a media format first.
    <button class="button is-ghost is-small" onclick={() => app.openSettings()}>Go to Settings</button>
  </p>
{:else if app.pendingImports.length > 0}
  <p class="has-text-grey">
    An import was started.
    <button class="button is-ghost is-small" onclick={() => app.go('/admin/library/import')}>Continue import</button>
  </p>
{:else}
  <div class="ff-import-row">
    <div class="select">
      <select bind:value={app.importSource}>
        <option value="folder">import folder</option>
        <option value="upload">upload files</option>
        <option value="plex">Plex library</option>
        <option value="jellyfin">Jellyfin library</option>
      </select>
    </div>
    {#if app.importSource !== 'plex' && app.importSource !== 'jellyfin'}
      <div class="select">
        <select bind:value={app.importCategory}>
          {#each app.categoryTree as c}
            <option value={c.name}>{treeMarker(c._depth)}{c.alias}</option>
          {/each}
        </select>
      </div>
    {/if}
    <button
      class="button is-primary"
      disabled={app.importSource !== 'plex' && app.importSource !== 'jellyfin' && !app.importCategory}
      onclick={() => app.startImport()}>Import</button>
  </div>
{/if}
