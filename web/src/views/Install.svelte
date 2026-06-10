<script>
  import { getContext } from 'svelte'
  import FolderBrowser from '../components/FolderBrowser.svelte'
  const app = getContext('app')
</script>

<form class="box ff-auth" onsubmit={(e) => app.doInstall(e)}>
  <h1 class="title is-4 has-text-centered">FileFin Setup</h1>
  <div class="field">
    <div class="control">
      <input class="input" placeholder="Admin username" bind:value={app.iuser} autocomplete="username" />
    </div>
  </div>
  <div class="field">
    <div class="control">
      <input class="input" type="password" placeholder="Password" bind:value={app.ipass} autocomplete="new-password" />
    </div>
  </div>
  <div class="field">
    <div class="control">
      <input class="input" type="number" placeholder="Server port" bind:value={app.iport} min="1" max="65535" />
    </div>
  </div>

  <div class="field">
    <p class="label is-small has-text-grey">Data folder</p>
    <div class="field has-addons">
      <div class="control is-expanded">
        <input class="input" placeholder="No folder selected" bind:value={app.dataDir} readonly />
      </div>
      <div class="control">
        <button type="button" class="button" onclick={() => app.openBrowser()}>Browse</button>
      </div>
    </div>
  </div>

  {#if app.browseOpen}
    <FolderBrowser
      title="Select data folder"
      path={app.browsePath}
      parent={app.browseParent}
      error={app.browseError}
      entries={app.browseEntries}
      onUp={() => app.navigate(app.browseParent)}
      onEntry={(e) => app.navigate(e.path)}
      onClose={() => (app.browseOpen = false)}
      onSelect={() => app.selectFolder()} />
  {/if}

  <div class="field">
    <button type="submit" class="button is-primary is-fullwidth" disabled={!app.dataDir}>Set up</button>
  </div>
  {#if app.installError}<p class="has-text-danger">{app.installError}</p>{/if}
</form>
