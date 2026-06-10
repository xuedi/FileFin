<script>
  import { getContext } from 'svelte'
  import { treeMarker } from '../../../lib/app.svelte.js'
  import FolderBrowser from '../../../components/FolderBrowser.svelte'
  const app = getContext('app')
</script>

<p class="label is-small has-text-grey">Plex database</p>
<div class="ff-import-row">
  <input class="input ff-grow" bind:value={app.plexDB} placeholder="path to com.plexapp.plugins.library.db" />
  <button type="button" class="button" onclick={() => app.openPlexBrowse()}>Browse</button>
  <button class="button" disabled={!app.plexDB.trim() || app.plexChecking} onclick={() => app.plexCheck()}>
    {app.plexChecking ? 'Checking...' : 'Check'}
  </button>
</div>
<details class="ff-advanced">
  <summary>Advanced</summary>
  <p class="label is-small has-text-grey">Metadata directory (override; defaults to the Plex Metadata folder)</p>
  <input class="input ff-grow" bind:value={app.plexMetaDir} placeholder="(auto)" />
</details>
{#if app.plexError}<p class="has-text-danger">{app.plexError}</p>{/if}

{#if app.plexBrowseOpen}
  <FolderBrowser
    title="Select the Plex database file"
    path={app.plexBrowse.path}
    parent={app.plexBrowse.parent}
    error={app.plexBrowseError}
    entries={app.plexBrowse.entries}
    onUp={() => app.plexBrowseNavigate(app.plexBrowse.parent)}
    onEntry={(e) => app.pickPlexDB(e)}
    onClose={() => (app.plexBrowseOpen = false)}
    entryLabel={(e) => (e.isDir ? e.name + '/' : e.name)} />
{/if}

{#if app.plexChecked}
  {#if app.plexStaging || app.plexProgress}
    <p class="label is-small has-text-grey">Loading media files</p>
    {@const total = app.plexProgress?.total || 0}
    {@const done = app.plexProgress?.done || 0}
    <progress class="progress is-primary" value={total ? Math.round((done / total) * 100) : 0} max="100"></progress>
    <p class="has-text-grey has-text-centered">{done} / {total} files - {app.plexProgress?.staged || 0} staged, {app.plexProgress?.missing || 0} missing</p>
  {:else}
    <table class="table is-fullwidth">
      <thead>
        <tr><th></th><th>Library</th><th>Items</th><th>Category</th><th>Path status</th></tr>
      </thead>
      <tbody>
        {#each app.plexRows as row}
          <tr>
            <td><input type="checkbox" checked={row.selected} onchange={() => app.togglePlexRow(row)} /></td>
            <td>{row.section}</td>
            <td>{row.count}</td>
            <td>
              <div class="select is-small">
                <select bind:value={row.categoryId}>
                  <option value={0}>Create category from Plex</option>
                  {#each app.categoryTree as c}
                    <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
                  {/each}
                </select>
              </div>
            </td>
            <td>
              {#if !row.selected}
                <span class="has-text-grey is-size-7">-</span>
              {:else if row.resolving}
                <span class="is-size-7">checking...</span>
              {:else if row.status === 'green'}
                <span class="has-text-success is-size-7" title={row.to ? 'resolved -> ' + row.to : ''}>
                  paths OK ({row.found}/{row.total}){#if row.to} &rarr; <code>{row.to}</code>{/if}
                </span>
              {:else if row.status === 'needsInput' || row.status === 'unresolved'}
                <div class="ff-plex-fix">
                  <span class="is-size-7" class:has-text-danger={row.status === 'unresolved'} class:has-text-warning-dark={row.status !== 'unresolved'}>
                    {row.status === 'unresolved' ? 'paths not found' : 'enter media location'}
                    {#if row.total}({row.found}/{row.total}){/if}
                  </span>
                  <input
                    class="input is-small ff-plex-base"
                    bind:value={row.searchBase}
                    placeholder="media location (folder the files live under)"
                    onkeydown={(e) => e.key === 'Enter' && app.plexResolveRow(row)} />
                  <button type="button" class="button is-small" disabled={!row.searchBase.trim() || row.resolving} onclick={() => app.plexResolveRow(row)}>Recheck</button>
                </div>
              {/if}
            </td>
          </tr>
        {/each}
        {#if app.plexRows.length === 0}
          <tr><td colspan="5" class="has-text-grey">No movie or show libraries found.</td></tr>
        {/if}
      </tbody>
    </table>
    <button class="button is-primary" disabled={!app.plexReady} onclick={() => app.startPlexStaging()}>Load media files</button>
  {/if}
{/if}
