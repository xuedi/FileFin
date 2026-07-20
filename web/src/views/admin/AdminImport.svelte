<script>
  import { getContext } from 'svelte'
  import { treeMarker, humanSize } from '../../lib/app.svelte.js'
  const app = getContext('app')

  // confidenceHint spells out why a row is not fully trusted, so the marker explains itself
  // instead of being a number the admin has to decode.
  const confidenceHint = (item) =>
    item.doubts?.length ? `Worth a look: ${item.doubts.join('; ')}` : 'Every check passed'
</script>

<div class="ff-page-head">
  <h1 class="title is-4">Import</h1>
  <div class="ff-head-actions">
    <button class="button is-small" onclick={() => app.openUploadImport()}>Upload files</button>
    <button class="button is-small" onclick={() => app.openPlexImport()}>Plex import</button>
    <button class="button is-small" onclick={() => app.openJellyfinImport()}>Jellyfin import</button>
  </div>
</div>
{#if app.mediaFormat === ''}
  <p class="has-text-grey">
    To import media, please select a media format first.
    <button class="button is-ghost is-small" onclick={() => app.openSettings()}>Go to Settings</button>
  </p>
{:else}
  {#if app.importFolderPath}
    <p class="label is-small has-text-grey">Media found in <code>{app.importFolderPath}</code></p>
  {:else}
    <p class="label is-small has-text-grey">
      No import folder is configured.
      <button class="button is-ghost is-small" onclick={() => app.openSettings()}>Go to Settings</button>
    </p>
  {/if}
  {#if app.importScanError}<p class="has-text-danger">{app.importScanError}</p>{/if}
  {#if app.importScanning}
    <p class="has-text-grey has-text-centered ff-loading">Reading the import folder...</p>
  {:else if app.importFolderPath}
    <table class="table is-fullwidth">
      <thead>
        <tr>
          <th title="How much recognition trusts this row; least trusted first">Sure</th>
          <th>Entry</th>
          <th title="Whether this was recognised as a show or a film">Kind</th>
          <th>Title</th>
          <th>Year</th>
          <th title="Video files that make up this media">Media files</th>
          <th title="Total size of the video files">Size</th>
          <th title="Poster found beside the media">Poster</th>
          <th title="Subtitle files found beside the media">Subs</th>
          <th title="Ticked when this media is already in the library">Dup</th>
          <th>Category</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each app.importItems as item (item.id)}
          <tr>
            <td class="has-text-centered">
              <span class="tag is-small ff-confidence-{item.confidence}" title={confidenceHint(item)}>
                {item.confidence || 'n/a'}
              </span>
            </td>
            <td class="ff-import-entry" title={item.entry}>{item.entry}{item.dir ? '/' : ''}</td>
            <td class="has-text-grey">{item.isShow ? 'show' : 'film'}</td>
            <td><input class="input is-small ff-inline-input" bind:value={item.title} /></td>
            <td><input class="input is-small ff-year-input" type="text" bind:value={item.year} /></td>
            <td>{item.files}</td>
            <td>{humanSize(item.bytes)}</td>
            <td>{#if item.hasPoster}<span class="has-text-success has-text-weight-bold" title="Poster found">&#10003;</span>{/if}</td>
            <td>{#if item.subCount > 0}<span class="has-text-success has-text-weight-bold" title="Subtitle files found">{item.subCount}</span>{/if}</td>
            <td class="has-text-centered">
              {#if item.duplicate}
                <span class="ff-dup-icon" title="Already in the library: {item.duplicate}">&#9888;</span>
              {/if}
            </td>
            <td>
              <div class="ff-import-category">
                <div class="select is-small">
                  <select bind:value={item.categoryId}>
                    {#each app.categoryTree as c}
                      <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
                    {/each}
                  </select>
                </div>
                {#if item.categoryReason}
                  <span class="tag is-small ff-guess" title={'Preselected because ' + item.categoryReason}>why</span>
                {/if}
              </div>
            </td>
            <td class="ff-row-actions">
              <button class="button is-small is-danger" title="Leave this media in the import folder" onclick={() => app.dropImportItem(item)}>X</button>
            </td>
          </tr>
        {/each}
        {#if app.importItems.length === 0}
          <tr><td colspan="12" class="has-text-grey">No media found in the import folder.</td></tr>
        {/if}
      </tbody>
    </table>
    <label class="checkbox ff-delete-after">
      <input type="checkbox" bind:checked={app.deleteAfter} />
      Delete originals from the import folder after a successful import
    </label>
    <label class="checkbox ff-delete-after ff-purge-folder" class:ff-disabled={!app.deleteAfter}>
      <input type="checkbox" bind:checked={app.purgeFolder} disabled={!app.deleteAfter} />
      Clean up also non imported media: delete all data in the import folder after the import
    </label>
    <div>
      <button class="button is-primary" disabled={!app.importReady} onclick={() => app.startFolderImport()}>Import</button>
    </div>

    <!-- Taken off the table with X, kept here so a mis-click (or a change of mind) costs one
         click instead of a rescan. -->
    {#if app.importSkipped.length > 0}
      <p class="label is-small has-text-grey ff-skipped-head">Not importing</p>
      <ul class="ff-skipped">
        {#each app.importSkipped as item (item.id)}
          <li>
            <button class="button is-small is-ghost ff-skipped-add" title="Put this back on the list" onclick={() => app.takeBackImportItem(item)}>+</button>
            <span class="ff-skipped-name">{item.entry}</span>
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
{/if}
