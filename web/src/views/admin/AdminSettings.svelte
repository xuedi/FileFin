<script>
  import { getContext } from 'svelte'
  import FolderBrowser from '../../components/FolderBrowser.svelte'
  const app = getContext('app')
</script>

<div class="ff-page-head">
  <h1 class="title is-4">Settings</h1>
  <div class="buttons">
    <button class="button" disabled={app.enrichScanning} onclick={() => app.enrichScan()}>
      {app.enrichScanning ? 'Scanning...' : 'OMDB enrichment'}
    </button>
    <button class="button" disabled={app.optimizeScanning} onclick={() => app.optimizeScan()}>
      {app.optimizeScanning ? 'Scanning...' : 'Optimizer scan'}
    </button>
    <button class="button" disabled={app.thumbnailScanning} onclick={() => app.thumbnailScan()}>
      {app.thumbnailScanning ? 'Scanning...' : 'Thumbnail scan'}
    </button>
    <button class="button" disabled={app.rebuilding} onclick={() => app.rebuildDb()}>
      {app.rebuilding ? 'Rebuilding...' : 'Rebuild database'}
    </button>
  </div>
</div>
{#if app.settingsError}<p class="has-text-danger">{app.settingsError}</p>{/if}
{#if app.rebuildMsg}<p class="has-text-link">{app.rebuildMsg}</p>{/if}
{#if app.optimizeScanMsg}<p class="has-text-link">{app.optimizeScanMsg}</p>{/if}
{#if app.enrichScanMsg}<p class="has-text-link">{app.enrichScanMsg}</p>{/if}
{#if app.thumbnailScanMsg}<p class="has-text-link">{app.thumbnailScanMsg}</p>{/if}
<table class="table is-fullwidth">
  <tbody>
    {#each app.settings as row}
      <tr>
        <td class="has-text-grey ff-settings-name">{row.name}</td>
        <td>
          {#if row.name === 'OMDb API key' && app.editOmdb}
            <input class="input is-small ff-inline-input" bind:value={app.omdbInput} onkeydown={(e) => e.key === 'Enter' && app.saveOmdbKey()} />
            <button class="button is-small is-primary" onclick={() => app.saveOmdbKey()}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editOmdb = false)}>Cancel</button>
          {:else if row.name === 'Log level' && app.editLogging}
            <div class="select is-small">
              <select bind:value={app.logLevelInput}>
                <option value="error">error</option>
                <option value="info">info</option>
                <option value="debug">debug</option>
              </select>
            </div>
          {:else if row.name === 'Log output' && app.editLogging}
            <input class="input is-small ff-inline-input" bind:value={app.logOutputInput} onkeydown={(e) => e.key === 'Enter' && app.saveLogging()} />
            <button class="button is-small is-primary" onclick={() => app.saveLogging()}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editLogging = false)}>Cancel</button>
          {:else if row.name === 'Transcoding' && app.editTranscoding}
            <label class="checkbox"><input type="checkbox" bind:checked={app.transcodeEnabledInput} /> enabled</label>
            <button class="button is-small is-primary" onclick={() => app.saveTranscoding()}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editTranscoding = false)}>Cancel</button>
          {:else if row.name === 'ffmpeg path' && app.editTranscoding}
            <input class="input is-small ff-inline-input" bind:value={app.ffmpegPathInput} onkeydown={(e) => e.key === 'Enter' && app.saveTranscoding()} />
          {:else if row.name === 'ffprobe path' && app.editTranscoding}
            <input class="input is-small ff-inline-input" bind:value={app.ffprobePathInput} onkeydown={(e) => e.key === 'Enter' && app.saveTranscoding()} />
          {:else if row.name === 'Subtitle language' && app.editSubtitle}
            <input class="input is-small ff-inline-input" bind:value={app.subtitleInput} onkeydown={(e) => e.key === 'Enter' && app.saveSubtitle()} />
            <button class="button is-small is-primary" onclick={() => app.saveSubtitle()}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editSubtitle = false)}>Cancel</button>
          {:else if row.name === 'Optimizer' && app.editOptimizer}
            <div class="select is-small">
              <select bind:value={app.optimizeModeInput}>
                {#each app.optimizeModes as m}
                  <option value={m.value}>{m.label}</option>
                {/each}
              </select>
            </div>
            <button class="button is-small is-primary" onclick={() => app.saveOptimizer()}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editOptimizer = false)}>Cancel</button>
          {:else}
            {row.value}
            {#if row.name === 'Import folder'}
              <button class="button is-small ff-settings-edit" onclick={() => app.openImportFolderBrowser()}>Edit</button>
            {:else if row.name === 'OMDb API key'}
              <button class="button is-small ff-settings-edit" onclick={() => app.startEditOmdb()}>Edit</button>
            {:else if row.name === 'Log level'}
              <button class="button is-small ff-settings-edit" onclick={() => app.startEditLogging()}>Edit</button>
            {:else if row.name === 'Log output'}
              <button class="button is-small ff-settings-edit" onclick={() => app.startEditLogging()}>Edit</button>
            {:else if row.name === 'Transcoding'}
              <button class="button is-small ff-settings-edit" onclick={() => app.startEditTranscoding()}>Edit</button>
            {:else if row.name === 'Subtitle language'}
              <button class="button is-small ff-settings-edit" onclick={() => app.startEditSubtitle()}>Edit</button>
            {:else if row.name === 'Optimizer'}
              <button class="button is-small ff-settings-edit" onclick={() => app.startEditOptimizer()}>Edit</button>
            {/if}
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>
{#if app.ifBrowseOpen}
  <FolderBrowser
    title="Select import folder"
    path={app.ifPath}
    parent={app.ifParent}
    error={app.ifError}
    entries={app.ifEntries}
    onUp={() => app.importFolderNavigate(app.ifParent)}
    onEntry={(e) => app.importFolderNavigate(e.path)}
    onClose={() => (app.ifBrowseOpen = false)}
    onSelect={() => app.selectImportFolder()} />
{/if}
