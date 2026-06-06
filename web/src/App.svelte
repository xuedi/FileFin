<script>
  import { onMount } from 'svelte'
  import { api, Unauthorized } from './api.js'

  let booting = $state(true)
  let loggedIn = $state(false)
  let loginError = $state('')
  let username = $state('')
  let password = $state('')

  let categories = $state([])
  let activeCat = $state(null)
  let mediaList = $state([])
  let detail = $state(null)
  let currentFile = $state(0)
  let playing = $state(false)

  onMount(async () => {
    try {
      categories = await api('/api/categories')
      loggedIn = true
      await route()
    } catch (e) {
      if (!(e instanceof Unauthorized)) console.error(e)
    } finally {
      booting = false
    }
    window.addEventListener('popstate', route)
  })

  async function doLogin(e) {
    e.preventDefault()
    loginError = ''
    try {
      await api('/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      categories = await api('/api/categories')
      loggedIn = true
      password = ''
      await route()
    } catch (e) {
      loginError = 'Invalid credentials'
    }
  }

  async function logout() {
    await api('/api/logout', { method: 'POST' })
    loggedIn = false
    categories = []
    showHome()
    history.pushState({}, '', '/')
  }

  // --- state setters (no history changes) ---
  function showHome() {
    activeCat = null
    detail = null
    mediaList = []
  }

  async function showCategory(name) {
    activeCat = name
    detail = null
    mediaList = await api('/api/categories/' + encodeURIComponent(name) + '/media')
  }

  async function showMedia(id) {
    detail = await api('/api/media/' + id)
    currentFile = 0
    playing = false
  }

  // --- click handlers (push a history entry, then update the URL) ---
  async function openCategory(name) {
    await showCategory(name)
    history.pushState({}, '', '/' + encodeURIComponent(name) + '/')
  }

  async function openMedia(m) {
    await showMedia(m.id)
    history.pushState({}, '', '/' + encodeURIComponent(activeCat) + '/' + slugFor(m) + '/')
  }

  function slugFor(m) {
    // e.g. "1966-Django", "1968-Once-Upon-a-Time-in-the-West".
    const title = (m.title || '').replace(/[^A-Za-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
    if (!title) return m.id // non-Latin titles slugify to nothing; fall back to the id
    return m.year ? m.year + '-' + title : title
  }

  // --- restore view from the current URL (browser back/forward, deep links) ---
  async function route() {
    const segs = location.pathname.split('/').filter(Boolean).map(decodeURIComponent)
    if (segs.length === 0) {
      showHome()
      return
    }
    await showCategory(segs[0])
    if (segs.length >= 2) {
      const m = mediaList.find((x) => slugFor(x) === segs[1])
      if (m) await showMedia(m.id)
    }
  }

  function playFile(idx) {
    currentFile = idx
    playing = true
  }

  function fileLabel(f) {
    if (f.season || f.episode) return `S${f.season} E${f.episode} - ${f.name}`
    return f.name
  }
</script>

{#if booting}
  <p class="center">Loading…</p>
{:else if !loggedIn}
  <form class="login" onsubmit={doLogin}>
    <h1>FileFin</h1>
    <input placeholder="Username" bind:value={username} autocomplete="username" />
    <input type="password" placeholder="Password" bind:value={password} autocomplete="current-password" />
    <button type="submit">Sign in</button>
    {#if loginError}<p class="error">{loginError}</p>{/if}
  </form>
{:else}
  <header>
    <strong>FileFin</strong>
    <button class="link" onclick={logout}>Sign out</button>
  </header>
  <div class="layout">
    <nav>
      {#each categories as c}
        <button class:active={c.name === activeCat} onclick={() => openCategory(c.name)}>
          {c.name} <span class="count">{c.count}</span>
        </button>
      {/each}
    </nav>

    <main>
      {#if detail}
        <button class="link" onclick={() => history.back()}>← Back</button>
        {#if detail.hasBanner}
          <img class="banner" src={'/api/media/' + detail.id + '/banner'} alt="" />
        {/if}
        <div class="titlebar">
          <h2>{detail.title} <span class="year">({detail.year})</span></h2>
          {#if !playing}
            <button class="play" onclick={() => playFile(currentFile)}>▶ Play</button>
          {/if}
        </div>

        {#if playing}
          <video class="player" controls autoplay src={'/api/media/' + detail.id + '/file/' + currentFile}></video>
        {/if}

        {#if detail.files.length > 1}
          <div class="episodes">
            {#each detail.files as f}
              <button class:active={f.index === currentFile} onclick={() => playFile(f.index)}>
                {fileLabel(f)}
              </button>
            {/each}
          </div>
        {/if}

        {#if detail.description}<p>{detail.description}</p>{/if}
        {#if detail.tags.length}<p class="tags">{#each detail.tags as t}<span>{t}</span>{/each}</p>{/if}

        {#if detail.metadata.length}
          <table>
            <tbody>
              {#each detail.metadata as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
            </tbody>
          </table>
        {/if}

        {#if detail.actors.length}
          <h3>Cast</h3>
          <ul>{#each detail.actors as a}<li>{a}</li>{/each}</ul>
        {/if}

        {#if detail.plot}<h3>Plot</h3><p>{detail.plot}</p>{/if}
      {:else if activeCat}
        <div class="grid">
          {#each mediaList as m}
            <button class="card" onclick={() => openMedia(m)}>
              {#if m.hasPoster}
                <img src={'/api/media/' + m.id + '/poster'} alt={m.title} />
              {:else}
                <div class="noposter">{m.title}</div>
              {/if}
              <span>{m.title}</span>
              <span class="year">{m.year}</span>
            </button>
          {/each}
        </div>
      {:else}
        <p class="center">Pick a category.</p>
      {/if}
    </main>
  </div>
{/if}
