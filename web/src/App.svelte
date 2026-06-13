<script>
  import { onMount, setContext } from 'svelte'
  import { AppState, treeMarker } from './lib/app.svelte.js'
  import { api } from './lib/api.js'
  import Install from './views/Install.svelte'
  import Login from './views/Login.svelte'
  import LibraryView from './views/library/LibraryView.svelte'
  import TokPlayer from './views/library/TokPlayer.svelte'
  import AdminLibrary from './views/admin/AdminLibrary.svelte'
  import ImportWork from './views/admin/import/ImportWork.svelte'
  import FormatGate from './views/admin/FormatGate.svelte'
  import AdminSettings from './views/admin/AdminSettings.svelte'
  import AdminUsers from './views/admin/AdminUsers.svelte'
  import AdminProgress from './views/admin/AdminProgress.svelte'
  import AdminDashboard from './views/admin/AdminDashboard.svelte'
  import Toast from './components/Toast.svelte'

  const app = new AppState()
  setContext('app', app)

  onMount(async () => {
    window.addEventListener('popstate', () => app.route()) // browser back/forward restores the view
    // A tab/page close cannot await a fetch, so flush the last position via sendBeacon.
    const flush = () => {
      if (app.playing && app.detail) app.reportProgress(app.detail.id, app.currentFile, 'pagehide', true)
    }
    window.addEventListener('pagehide', flush)
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') flush()
    })
    try {
      const st = await api('/api/state')
      app.needsSetup = st.needsSetup
      if (app.needsSetup) {
        try {
          const r = await api('/api/install/browse') // defaults to the app's current directory
          app.dataDir = r.path
        } catch {
          app.dataDir = ''
        }
      } else {
        try {
          app.me = await api('/api/me')
          if (app.me?.admin) await app.loadSettings() // mediaFormat gates the Library import UI
          await app.route() // restore the view from the current URL (refresh / deep link)
        } catch {
          app.me = null
        }
      }
    } catch (e) {
      console.error(e)
    } finally {
      app.booting = false
    }
  })
</script>

{#if app.booting}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{:else if app.needsSetup}
  <Install />
{:else if !app.me}
  <Login />
{:else}
  <nav class="ff-navbar">
    <span class="ff-brand">FileFin</span>
    <div class="ff-navbar-actions">
      {#if app.me.admin}
        <div class="buttons has-addons ff-toggle">
          <button class="button is-small" class:is-link={app.view === 'library'} onclick={() => app.showLibrary()}>Library</button>
          <button class="button is-small" class:is-link={app.view === 'admin'} onclick={() => app.goAdmin()}>Admin</button>
        </div>
      {/if}
      <button class="button is-ghost is-small" onclick={() => app.signOut()}>Sign out</button>
    </div>
  </nav>
  <div class="ff-layout">
    <aside class="menu ff-sidebar">
      <ul class="menu-list">
        {#if app.view === 'library'}
          <li><a href={null} class:is-active={app.homeCategory === ''} onclick={() => app.go('/')}>Home</a></li>
          {#each app.homeTree as c}
            <li><a href={null} class:is-active={app.homeCategory === c.name} onclick={() => app.go('/category/' + c.id)}>{treeMarker(c._depth)}{c.alias}</a></li>
          {/each}
        {:else}
          <li><a href={null} class:is-active={app.adminView === 'dashboard'} onclick={() => app.go('/admin/dashboard')}>Dashboard</a></li>
          <li><a href={null} class:is-active={app.adminView === 'library'} onclick={() => app.openAdminLibrary()}>Library</a></li>
          <li><a href={null} class:is-active={app.adminView === 'users'} onclick={() => app.go('/admin/users')}>Users</a></li>
          <li><a href={null} class:is-active={app.adminView === 'settings'} onclick={() => app.openSettings()}>Settings</a></li>
          <li><a href={null} class:is-active={app.adminView === 'progress'} onclick={() => app.go('/admin/progress')}>Progress</a></li>
        {/if}
      </ul>
    </aside>
    <main class="ff-main">
      {#if app.view === 'library'}
        <LibraryView />
      {:else if app.adminView === 'library' && app.importPage === ''}
        <AdminLibrary />
      {:else if app.adminView === 'library'}
        <ImportWork />
      {:else if app.adminView === 'settings' && app.mediaFormat === ''}
        <FormatGate />
      {:else if app.adminView === 'settings'}
        <AdminSettings />
      {:else if app.adminView === 'users'}
        <AdminUsers />
      {:else if app.adminView === 'progress'}
        <AdminProgress />
      {:else}
        <AdminDashboard />
      {/if}
    </main>
  </div>
{/if}

<svelte:window onkeydown={(e) => app.tokKeydown(e)} />

{#if app.tokOn}
  <TokPlayer />
{/if}

<Toast />
