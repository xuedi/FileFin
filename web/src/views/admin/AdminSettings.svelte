<script>
  import { getContext } from 'svelte'
  import FolderBrowser from '../../components/FolderBrowser.svelte'
  const app = getContext('app')

  const tabs = [
    { id: 'system', label: 'System' },
    { id: 'library', label: 'Library' },
    { id: 'playback', label: 'Playback' },
    { id: 'automation', label: 'Automation' },
    { id: 'logging', label: 'Logging' },
    { id: 'maintenance', label: 'Maintenance' },
  ]

  const taskTypes = [
    { key: 'imports', label: 'Imports' },
    { key: 'optimize', label: 'Optimizer' },
    { key: 'enrich', label: 'Metadata' },
    { key: 'thumbnail', label: 'Thumbnails' },
    { key: 'probe', label: 'Probe' },
  ]
</script>

<h1 class="title is-4">Settings</h1>

<div class="tabs ff-settings-tabs">
  <ul>
    {#each tabs as t}
      <li class:is-active={app.settingsTab === t.id}>
        <a href={null} onclick={() => app.go('/admin/settings/' + t.id)}>{t.label}</a>
      </li>
    {/each}
  </ul>
</div>

{#if app.settingsTab === 'system'}
  <div class="ff-sys-grid">
    <div class="box ff-settings-readonly ff-sys-box">
      <h2 class="title is-5">Dashboard</h2>
      <table class="table is-fullwidth ff-sys-table">
        <tbody>
        <tr><td>Port</td><td>{app.sysPort}</td></tr>
        <tr><td>Data folder</td><td>{app.sysDataDir}</td></tr>
        <tr><td>Import folder</td><td>{app.importFolder || '(not set)'}</td></tr>
        <tr><td>Cache</td><td>SQLite ({app.sysCachePath})</td></tr>
        <tr><td>Media format</td><td>{app.formatBoxes.find((b) => b.id === app.mediaFormat)?.title ?? app.mediaFormat}</td></tr>
        <tr>
          <td>Users</td>
          <td>{app.sysUsers} account{app.sysUsers === 1 ? '' : 's'} <a href={null} onclick={() => app.go('/admin/users')}>Manage</a></td>
        </tr>
        <tr>
          <td>Discovery</td>
          <td>
            {app.discoveryStatus}
            {#if app.discoveryRunning}
              <span class="has-text-grey ff-force-now">running...</span>
            {:else}
              <a href={null} class="ff-force-now" onclick={() => app.runDiscovery()}>force now</a>
            {/if}
          </td>
        </tr>
        </tbody>
      </table>
      <p class="help">These were set during installation and are read-only here.</p>
    </div>
    <div class="box ff-sys-box">
      <h2 class="title is-5">Tasks</h2>
      <table class="table is-fullwidth ff-sys-table">
        <tbody>
          {#each taskTypes as t}
            <tr><td>{t.label}</td><td class="ff-task-count">{app.tasks ? app.tasks[t.key] : '-'}</td></tr>
          {/each}
        </tbody>
      </table>
      <p class="help">Outstanding background tasks per type (queued + running).</p>
    </div>
  </div>
{:else if app.settingsTab === 'library'}
  <div class="box ff-settings-card">
    <div class="field">
      <label class="label" for="ff-import-folder">Import folder</label>
      <div class="field has-addons">
        <div class="control is-expanded">
          <input id="ff-import-folder" class="input" readonly value={app.importFolder || '(not set)'} />
        </div>
        <div class="control">
          <button type="button" class="button" onclick={() => app.openImportFolderBrowser()}>Browse...</button>
        </div>
      </div>
      <p class="help">Server folder media is imported from.</p>
    </div>
    <div class="field">
      <label class="label" for="ff-omdb">OMDb API key</label>
      <div class="control">
        <input id="ff-omdb" class="input" bind:value={app.omdbKey} placeholder="(not set - enrichment disabled)" />
      </div>
      <p class="help">Key for metadata lookups. Leave empty to disable OMDb enrichment.</p>
    </div>
    <div class="ff-settings-actions">
      <button class="button is-ghost" disabled={!app.libraryDirty} onclick={() => app.resetTab('library')}>Reset</button>
      <button class="button is-primary" disabled={!app.libraryDirty} onclick={() => app.saveLibrary()}>Save</button>
    </div>
  </div>
{:else if app.settingsTab === 'playback'}
  <div class="box ff-settings-card">
    <div class="field">
      <p class="label">Transcoding</p>
      <div class="control">
        <label class="checkbox"><input type="checkbox" bind:checked={app.transcodeEnabled} /> Enabled</label>
      </div>
      <p class="help">Transcode non-native files on the fly during playback.</p>
    </div>
    <div class="field">
      <label class="label" for="ff-ffmpeg">ffmpeg path</label>
      <div class="control"><input id="ff-ffmpeg" class="input" bind:value={app.ffmpegPath} /></div>
      <p class="help">Leave as "ffmpeg" to use the binary on PATH.</p>
    </div>
    <div class="field">
      <label class="label" for="ff-ffprobe">ffprobe path</label>
      <div class="control"><input id="ff-ffprobe" class="input" bind:value={app.ffprobePath} /></div>
      <p class="help">Leave as "ffprobe" to use the binary on PATH.</p>
    </div>
    <div class="field">
      <label class="label" for="ff-sublang">Subtitle language</label>
      <div class="control"><input id="ff-sublang" class="input ff-narrow" bind:value={app.subtitleLanguage} /></div>
      <p class="help">Preferred sidecar subtitle language, e.g. "en".</p>
    </div>
    <div class="ff-settings-actions">
      <button class="button is-ghost" disabled={!app.playbackDirty} onclick={() => app.resetTab('playback')}>Reset</button>
      <button class="button is-primary" disabled={!app.playbackDirty} onclick={() => app.savePlayback()}>Save</button>
    </div>
  </div>
{:else if app.settingsTab === 'automation'}
  <div class="box ff-settings-card">
    <div class="field">
      <label class="label" for="ff-optimizer">Optimizer</label>
      <div class="control">
        <div class="select">
          <select id="ff-optimizer" bind:value={app.optimizeMode}>
            {#each app.optimizeModes as m}<option value={m.value}>{m.label}</option>{/each}
          </select>
        </div>
      </div>
      <p class="help">Pre-transcode files in the background. GPU uses the best hardware encoder.</p>
    </div>
    <div class="field">
      <label class="label" for="ff-discovery">Discovery</label>
      <div class="control">
        <div class="select">
          <select id="ff-discovery" bind:value={app.discoveryInterval}>
            {#each app.discoveryIntervals as d}<option value={d.value}>{d.label}</option>{/each}
          </select>
        </div>
      </div>
      <p class="help">How often the background agent reconciles disk and checks media health.</p>
    </div>
    <div class="ff-settings-actions">
      <button class="button is-ghost" disabled={!app.automationDirty} onclick={() => app.resetTab('automation')}>Reset</button>
      <button class="button is-primary" disabled={!app.automationDirty} onclick={() => app.saveAutomation()}>Save</button>
    </div>
  </div>
{:else if app.settingsTab === 'logging'}
  <div class="box ff-settings-card">
    <div class="field">
      <label class="label" for="ff-loglevel">Log level</label>
      <div class="control">
        <div class="select">
          <select id="ff-loglevel" bind:value={app.logLevel}>
            <option value="error">error</option>
            <option value="info">info</option>
            <option value="debug">debug</option>
          </select>
        </div>
      </div>
    </div>
    <div class="field">
      <label class="label" for="ff-logoutput">Log output</label>
      <div class="control"><input id="ff-logoutput" class="input" bind:value={app.logOutput} /></div>
      <p class="help">STDOUT, STDERR, or an absolute file path.</p>
    </div>
    <div class="ff-settings-actions">
      <button class="button is-ghost" disabled={!app.loggingDirty} onclick={() => app.resetTab('logging')}>Reset</button>
      <button class="button is-primary" disabled={!app.loggingDirty} onclick={() => app.saveLogging()}>Save</button>
    </div>
  </div>
{:else if app.settingsTab === 'maintenance'}
  <div class="box ff-settings-card">
    <div class="ff-maint-row">
      <div class="ff-maint-text"><strong>Re-scan metadata (OMDb)</strong><p class="help">Queue media missing or stale OMDb metadata.</p></div>
      <button class="button" disabled={app.enrichScanning} onclick={() => app.enrichScan()}>{app.enrichScanning ? 'Scanning...' : 'Run'}</button>
    </div>
    <div class="ff-maint-row">
      <div class="ff-maint-text"><strong>Re-scan optimizer</strong><p class="help">Queue files to pre-transcode in the background.</p></div>
      <button class="button" disabled={app.optimizeScanning} onclick={() => app.optimizeScan()}>{app.optimizeScanning ? 'Scanning...' : 'Run'}</button>
    </div>
    <div class="ff-maint-row">
      <div class="ff-maint-text"><strong>Re-scan thumbnails</strong><p class="help">Queue media missing poster thumbnails.</p></div>
      <button class="button" disabled={app.thumbnailScanning} onclick={() => app.thumbnailScan()}>{app.thumbnailScanning ? 'Scanning...' : 'Run'}</button>
    </div>
    <div class="ff-maint-row">
      <div class="ff-maint-text"><strong>Re-scan formats (probe)</strong><p class="help">Queue files whose true container/codecs need probing.</p></div>
      <button class="button" disabled={app.probeScanning} onclick={() => app.probeScan()}>{app.probeScanning ? 'Scanning...' : 'Run'}</button>
    </div>
    <p class="help ff-maint-note">Scan progress shows on the <a href={null} onclick={() => app.go('/admin/progress')}>Progress</a> page.</p>
  </div>
  <div class="box ff-settings-card ff-danger-zone">
    <h2 class="ff-danger-title">Danger zone</h2>
    <div class="ff-maint-row">
      <div class="ff-maint-text"><strong>Rebuild database</strong><p class="help">Flush the cache and rebuild it from the data folder. Also clears pending imports.</p></div>
      <button class="button is-danger" disabled={app.rebuilding} onclick={() => app.rebuildDb()}>{app.rebuilding ? 'Rebuilding...' : 'Rebuild'}</button>
    </div>
  </div>
{/if}

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
