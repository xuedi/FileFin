<script>
  import { getContext } from 'svelte'
  import { treeMarker } from '../../../lib/app.svelte.js'
  const app = getContext('app')
</script>

<!-- assessment of the staged files (configured folder or uploaded set) -->
{#if app.importOrigin === 'plex'}
  <p class="label is-small has-text-grey">Assessment of the Plex import</p>
{:else if app.importOrigin === 'jellyfin'}
  <p class="label is-small has-text-grey">Assessment of the Jellyfin import</p>
{:else if app.importOrigin === 'upload'}
  <p class="label is-small has-text-grey">Assessment of the uploaded files</p>
{:else}
  <p class="label is-small has-text-grey">Assessment of <code>{app.importFolder}</code></p>
{/if}
{#if app.assessError}<p class="has-text-danger">{app.assessError}</p>{/if}
{#if app.assessLoading}
  <p class="has-text-grey has-text-centered ff-loading">Scanning and looking up metadata...</p>
{:else}
  <table class="table is-fullwidth">
    <thead>
      <tr><th>Category</th><th>Media files</th><th>Title</th><th>Year</th><th>Poster</th><th>Subs</th><th></th></tr>
    </thead>
    <tbody>
      {#each app.assessGroups as group}
        <tr>
          <td>
            {#if app.editKey === group.key}
              <div class="select is-small">
                <select bind:value={app.editCategory}>
                  {#each app.categoryTree as c}
                    <option value={c.name}>{treeMarker(c._depth)}{c.alias}</option>
                  {/each}
                </select>
              </div>
            {:else}
              {app.categoryAlias(group.category)}
            {/if}
          </td>
          <td>{group.count}</td>
          <td>
            {#if app.editKey === group.key}
              <input class="input is-small ff-inline-input" bind:value={app.editTitle} onkeydown={(e) => e.key === 'Enter' && app.saveImportEdit()} />
            {:else}
              {group.title || '(unknown)'}
              {#if group.duplicate}
                <span class="tag is-warning is-light" title="Already in the library: {group.duplicate}">in library</span>
              {/if}
            {/if}
          </td>
          <td>
            {#if app.editKey === group.key}
              <input class="input is-small ff-year-input" type="text" bind:value={app.editYear} onkeydown={(e) => e.key === 'Enter' && app.saveImportEdit()} />
            {:else}
              {group.year || ''}
            {/if}
          </td>
          <td>{#if group.hasPoster}<span class="has-text-success has-text-weight-bold" title="Poster found">&#10003;</span>{/if}</td>
          <td>{#if group.subCount > 0}<span class="has-text-success has-text-weight-bold" title="Subtitle files found">{group.subCount}</span>{/if}</td>
          <td class="ff-row-actions">
            {#if app.editKey === group.key}
              <button class="button is-small is-primary" onclick={() => app.saveImportEdit()}>Save</button>
              <button class="button is-small is-ghost" onclick={() => (app.editKey = '')}>Cancel</button>
            {:else}
              <button class="button is-small" onclick={() => app.startEditImport(group)}>Edit</button>
              <button class="button is-small is-danger" title="Remove this media from the import" onclick={() => app.deleteImportRow(group)}>X</button>
            {/if}
          </td>
        </tr>
      {/each}
      {#if app.assessGroups.length === 0}
        <tr><td colspan="7" class="has-text-grey">No media files found.</td></tr>
      {/if}
    </tbody>
  </table>
  <label class="checkbox ff-delete-after">
    <input
      type="checkbox"
      bind:checked={app.deleteAfter}
      disabled={app.importOrigin === 'upload' || app.importOrigin === 'plex'} />
    {#if app.importOrigin === 'plex'}
      Plex originals are never touched
    {:else if app.importOrigin === 'upload'}
      Uploaded files are always removed after a successful import
    {:else}
      Delete originals from the import folder after a successful import
    {/if}
  </label>
  {#if app.assessDuplicates > 0}
    <p class="has-text-warning ff-dup-warning">
      {app.assessDuplicates} of these are already in the library - remove them with X unless you mean to import them again.
    </p>
  {/if}
  <div>
    <button class="button is-primary" disabled={app.assessRows.length === 0} onclick={() => app.startImportBatch()}>Start import</button>
  </div>
{/if}
