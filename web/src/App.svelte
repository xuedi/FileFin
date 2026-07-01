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
  import AdminStats from './views/admin/AdminStats.svelte'
  import UnhealthyMedia from './views/admin/UnhealthyMedia.svelte'
  import UserSettings from './views/settings/UserSettings.svelte'
  import Toast from './components/Toast.svelte'
  import GithubLink from './components/GithubLink.svelte'

  const app = new AppState()
  setContext('app', app)

  onMount(async () => {
    window.addEventListener('popstate', () => app.route()) // browser back/forward restores the view
    window.addEventListener('click', () => { if (app.userMenuOpen) app.userMenuOpen = false }) // outside-click closes the user menu
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
      app.version = st.version || ''
      if (app.needsSetup) {
        app.captureSetupToken() // read ?token= from the install URL before any setup call
        try {
          // defaults to the app's current directory; token-gated in install mode
          const r = await api('/api/install/browse', { headers: app.setupHeaders() })
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
  <nav class="navbar ff-navbar" aria-label="main navigation">
    <div class="navbar-brand">
      <a href={null} class="navbar-item ff-brand" onclick={() => app.showLibrary()}>FileFin</a>
    </div>
    <div class="navbar-menu">
      <div class="navbar-end">
        <div class="navbar-item has-dropdown" class:is-active={app.userMenuOpen}>
          <a href={null} class="navbar-link" onclick={(e) => { e.stopPropagation(); app.userMenuOpen = !app.userMenuOpen }}>{app.me.alias || app.me.user}</a>
          <div class="navbar-dropdown is-right">
            <a href={null} class="navbar-item" class:is-active={app.view === 'settings'} onclick={() => { app.userMenuOpen = false; app.goSettings() }}>Settings</a>
            {#if app.me.admin}
              <a href={null} class="navbar-item" class:is-active={app.view === 'admin'} onclick={() => { app.userMenuOpen = false; app.goAdmin() }}>Admin</a>
            {/if}
            <hr class="navbar-divider" />
            <a href={null} class="navbar-item" onclick={() => { app.userMenuOpen = false; app.signOut() }}>Sign out</a>
          </div>
        </div>
      </div>
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
        {:else if app.view === 'settings'}
          <li><a href={null} class="is-active">Account</a></li>
        {:else}
          <li><a href={null} class:is-active={app.adminView === 'dashboard'} onclick={() => app.go('/admin/dashboard')}>Dashboard</a></li>
          <li><a href={null} class:is-active={app.adminView === 'stats'} onclick={() => app.go('/admin/stats')}>Statistics</a></li>
          <li><a href={null} class:is-active={app.adminView === 'library'} onclick={() => app.openAdminLibrary()}>Library</a></li>
          <li><a href={null} class:is-active={app.adminView === 'users'} onclick={() => app.go('/admin/users')}>Users</a></li>
          <li><a href={null} class:is-active={app.adminView === 'unhealthy'} onclick={() => app.go('/admin/unhealthy')}>Unhealthy media</a></li>
          <li><a href={null} class:is-active={app.adminView === 'settings'} onclick={() => app.openSettings()}>Settings</a></li>
          <li><a href={null} class:is-active={app.adminView === 'progress'} onclick={() => app.go('/admin/progress')}>Progress</a></li>
        {/if}
      </ul>
      <div class="ff-sidebar-foot">
        <GithubLink version={app.version} />
      </div>
    </aside>
    <main class="ff-main">
      {#if app.view === 'library'}
        <LibraryView />
      {:else if app.view === 'settings'}
        <UserSettings />
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
      {:else if app.adminView === 'stats'}
        <AdminStats />
      {:else if app.adminView === 'unhealthy'}
        <UnhealthyMedia />
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
