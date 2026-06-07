<script>
  import { onMount } from 'svelte'
  import { api, Unauthorized } from './api.js'
  import Icon from './Icon.svelte'
  import { faHeart as faHeartSolid, faXmark, faCheck } from '@fortawesome/free-solid-svg-icons'
  import { faHeart as faHeartOutline } from '@fortawesome/free-regular-svg-icons'

  let booting = $state(true)
  let loggedIn = $state(false)
  let loginError = $state('')
  let username = $state('')
  let password = $state('')
  let me = $state(null)

  // Admin area: a toggle in the top bar flips the whole UI between library and admin
  // mode. Only admins ever see the toggle; the server enforces the admin API regardless.
  const adminPages = [
    { id: 'dashboard', label: 'Dashboard' },
    { id: 'users', label: 'Users' },
    { id: 'optimizer', label: 'Optimizer queue' },
  ]
  let adminMode = $state(false)
  let adminPage = $state('dashboard')
  let adminData = $state(null)

  let categories = $state([])
  let activeCat = $state(null)
  let mediaList = $state([])
  let continueList = $state([])
  let favoritesList = $state([])
  let completedList = $state([])
  let detail = $state(null)
  let currentFile = $state(0)
  let currentSeason = $state(null)
  let playing = $state(false)
  let videoEl = $state(null)
  let hls = null
  let pendingSeek = 0

  // Episodes grouped by season, ordered. Movies (single file, no numbering) yield
  // one group with season 0, which the UI renders without a season selector.
  const seasons = $derived(groupSeasons(detail))
  const currentEpisodes = $derived(seasons.find((s) => s.season === currentSeason)?.episodes ?? [])
  // "Continue" rather than "Play" when there is an unfinished resume point.
  const hasResume = $derived(!!detail && !detail.watched && (detail.continueIndex > 0 || detail.continueSeconds > 0))

  // Wire up playback whenever the player appears or the chosen file changes.
  // Direct-play files get a plain src; transcode files load HLS via the browser's
  // native HLS on Safari, or hls.js elsewhere. hls.js is imported lazily so its
  // weight only loads when a transcode file is actually played.
  $effect(() => {
    if (!playing || !videoEl || !detail) return
    const mediaId = detail.id
    const file = currentFile // captured so progress reports name the right file after a switch
    const seekTo = pendingSeek
    pendingSeek = 0

    const base = '/api/media/' + mediaId + '/file/' + file
    const f = detail.files.find((x) => x.index === file)
    let cancelled = false
    if (!f?.transcode) {
      videoEl.src = base
    } else {
      const url = base + '/hls/index.m3u8'
      if (videoEl.canPlayType('application/vnd.apple.mpegurl')) {
        videoEl.src = url
      } else {
        import('hls.js').then(({ default: Hls }) => {
          if (cancelled || !videoEl) return
          if (Hls.isSupported()) {
            hls = new Hls()
            hls.loadSource(url)
            hls.attachMedia(videoEl)
          } else {
            videoEl.src = url
          }
        })
      }
    }

    // Subtitles are independent of the stream, so native <track>s work for both
    // direct-play and HLS. Clear any from the previous file, then add this file's.
    videoEl.querySelectorAll('track').forEach((t) => t.remove())
    for (const sub of f?.subtitles ?? []) {
      const track = document.createElement('track')
      track.kind = 'subtitles'
      track.srclang = sub.lang
      track.label = sub.label || sub.lang
      track.src = base + '/subtitle/' + sub.index
      videoEl.appendChild(track)
    }

    const onMeta = () => {
      if (seekTo > 0 && videoEl && videoEl.currentTime < seekTo) videoEl.currentTime = seekTo
    }
    let lastMark = 0
    const onTime = () => {
      if (videoEl && Math.abs(videoEl.currentTime - lastMark) >= 30) {
        lastMark = videoEl.currentTime
        reportProgress(mediaId, file, 'checkpoint')
      }
    }
    const onPause = () => reportProgress(mediaId, file, 'pause')
    const onEnded = () => reportProgress(mediaId, file, 'ended')
    videoEl.addEventListener('loadedmetadata', onMeta, { once: true })
    videoEl.addEventListener('timeupdate', onTime)
    videoEl.addEventListener('pause', onPause)
    videoEl.addEventListener('ended', onEnded)

    return () => {
      cancelled = true
      reportProgress(mediaId, file, 'stop')
      if (videoEl) {
        videoEl.removeEventListener('loadedmetadata', onMeta)
        videoEl.removeEventListener('timeupdate', onTime)
        videoEl.removeEventListener('pause', onPause)
        videoEl.removeEventListener('ended', onEnded)
        videoEl.querySelectorAll('track').forEach((t) => t.remove())
      }
      if (hls) {
        hls.destroy()
        hls = null
      }
    }
  })

  // Best-effort progress report; failures are ignored. sendBeacon is used for
  // page/tab teardown where a normal fetch may be cancelled.
  function reportProgress(mediaId, file, event, useBeacon = false) {
    if (!videoEl) return
    const position = videoEl.currentTime
    const duration = videoEl.duration
    if (!duration || !isFinite(duration)) return
    const body = JSON.stringify({ file, position, duration, event })
    const url = '/api/media/' + mediaId + '/progress'
    if (useBeacon && navigator.sendBeacon) {
      navigator.sendBeacon(url, new Blob([body], { type: 'application/json' }))
      return
    }
    api(url, { method: 'POST', headers: { 'content-type': 'application/json' }, body }).catch(() => {})
  }

  onMount(async () => {
    try {
      me = await api('/api/me')
      categories = await api('/api/categories')
      loggedIn = true
      await route()
    } catch (e) {
      if (!(e instanceof Unauthorized)) console.error(e)
    } finally {
      booting = false
    }
    window.addEventListener('popstate', route)
    // A tab/page close cannot await a fetch, so flush the last position via sendBeacon.
    const flush = () => {
      if (playing && detail) reportProgress(detail.id, currentFile, 'pagehide', true)
    }
    window.addEventListener('pagehide', flush)
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') flush()
    })
  })

  async function doLogin(e) {
    e.preventDefault()
    loginError = ''
    try {
      me = await api('/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      categories = await api('/api/categories')
      loggedIn = true
      password = ''
      // Always land on Home after login, regardless of the URL the login page sat on.
      history.replaceState({}, '', '/')
      await showHome()
    } catch (e) {
      loginError = 'Invalid credentials'
    }
  }

  async function logout() {
    await api('/api/logout', { method: 'POST' })
    loggedIn = false
    me = null
    adminMode = false
    categories = []
    continueList = []
    favoritesList = []
    completedList = []
    activeCat = null
    detail = null
    mediaList = []
    history.pushState({}, '', '/')
  }

  async function openHome() {
    if (adminMode) adminMode = false
    await showHome()
    history.pushState({}, '', '/')
  }

  async function toggleAdmin() {
    if (adminMode) {
      await openHome() // leaving admin returns to the library home
    } else {
      adminMode = true
      await openAdminPage(adminPage)
    }
  }

  // loadAdminPage fetches a page's data without touching history (used on route restore).
  async function loadAdminPage(id) {
    adminPage = id
    adminData = null
    const paths = {
      optimizer: '/api/admin/optimizer',
      users: '/api/admin/users',
      dashboard: '/api/admin/summary',
    }
    adminData = await api(paths[id] || paths.dashboard)
  }

  // openAdminPage is the click/toggle entry: load, then record the URL so F5 stays here.
  async function openAdminPage(id) {
    await loadAdminPage(id)
    history.pushState({}, '', '/admin/' + id)
  }

  // --- state setters (no history changes) ---
  // Home is the per-user library landing: a "continue watching" row and a "favorites" row.
  async function showHome() {
    activeCat = null
    detail = null
    mediaList = []
    ;[continueList, favoritesList, completedList] = await Promise.all([
      api('/api/continue'),
      api('/api/favorites'),
      api('/api/completed'),
    ])
  }

  async function toggleFavorite() {
    const next = !detail.favorite
    detail.favorite = next // optimistic
    try {
      await api('/api/media/' + detail.id + '/favorite', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ favorite: next }),
      })
    } catch {
      detail.favorite = !next // revert on failure
    }
  }

  // Home tile "x": remove from Continue watching by clearing the resume pointer.
  async function removeFromContinue(m) {
    continueList = continueList.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/progress', { method: 'DELETE' }).catch(() => {})
  }

  // Home tile "x": remove from Favorites by clearing the favorite flag.
  async function removeFromFavorites(m) {
    favoritesList = favoritesList.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/favorite', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ favorite: false }),
    }).catch(() => {})
  }

  // Home tile "x": remove from Completed by clearing the watched (and resume) state.
  async function removeFromCompleted(m) {
    completedList = completedList.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/watched', { method: 'DELETE' }).catch(() => {})
  }

  async function showCategory(name) {
    activeCat = name
    detail = null
    mediaList = await api('/api/categories/' + encodeURIComponent(name) + '/media')
  }

  async function showMedia(id) {
    detail = await api('/api/media/' + id)
    playing = false
    // Preselect the furthest reached file (Continue); a fully watched folder starts over.
    const start = detail.watched ? 0 : detail.continueIndex || 0
    currentFile = detail.files[start]?.index ?? 0
    const groups = groupSeasons(detail)
    currentSeason = seasonOfFile(groups, currentFile, groups.length ? groups[0].season : null)
  }

  function seasonOfFile(groups, idx, fallback) {
    for (const g of groups) {
      if (g.episodes.some((e) => e.index === idx)) return g.season
    }
    return fallback
  }

  // --- click handlers (push a history entry, then update the URL) ---
  async function openCategory(name) {
    await showCategory(name)
    history.pushState({}, '', '/' + encodeURIComponent(name) + '/')
  }

  async function openMedia(m) {
    await showMedia(m.id)
    // From the home grid there is no active category; use the loaded item's own category
    // so the URL (and Back/deep-link) still resolves.
    const cat = activeCat || detail.category
    history.pushState({}, '', '/' + encodeURIComponent(cat) + '/' + slugFor(m) + '/')
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
    // "admin" is a reserved first segment: /admin/<page>. Non-admins fall back to home.
    if (segs[0] === 'admin') {
      if (me?.admin) {
        adminMode = true
        await loadAdminPage(adminPages.some((p) => p.id === segs[1]) ? segs[1] : 'dashboard')
        return
      }
      await showHome()
      return
    }
    adminMode = false
    if (segs.length === 0) {
      await showHome()
      return
    }
    await showCategory(segs[0])
    if (segs.length >= 2) {
      const m = mediaList.find((x) => slugFor(x) === segs[1])
      if (m) await showMedia(m.id)
    }
  }

  function playFile(idx) {
    // Resume to the saved second only when starting the furthest-unfinished file;
    // an explicit pick of any other file starts it from the beginning.
    const resumeIdx = detail && !detail.watched ? detail.continueIndex || 0 : -1
    pendingSeek = idx === (detail?.files[resumeIdx]?.index ?? -1) ? detail.continueSeconds || 0 : 0
    currentFile = idx
    playing = true
  }

  function groupSeasons(d) {
    if (!d || !d.files) return []
    const map = new Map()
    for (const f of d.files) {
      const s = f.season || 0
      if (!map.has(s)) map.set(s, [])
      map.get(s).push(f)
    }
    return [...map.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([season, episodes]) => {
        const sorted = episodes.sort((a, b) => (a.episode || 0) - (b.episode || 0))
        return { season, episodes: sorted, watched: sorted.every((e) => e.watched) }
      })
  }

  // Short chip label for an episode: "E1" when numbered, else the file name.
  function episodeLabel(f) {
    return f.episode ? 'E' + f.episode : f.name
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
    <div class="header-actions">
      {#if me?.admin}
        <button class="link" onclick={toggleAdmin}>{adminMode ? 'Library' : 'Admin'}</button>
      {/if}
      <button class="link" onclick={logout}>Sign out</button>
    </div>
  </header>
  <div class="layout">
    <nav>
      {#if adminMode}
        {#each adminPages as p}
          <button class:active={p.id === adminPage} onclick={() => openAdminPage(p.id)}>{p.label}</button>
        {/each}
      {:else}
        <button class="home-link" class:active={!activeCat && !detail} onclick={openHome}>Home</button>
        {#each categories as c}
          <button class:active={c.name === activeCat} onclick={() => openCategory(c.name)}>
            {c.name} <span class="count">{c.count}</span>
          </button>
        {/each}
      {/if}
    </nav>

    <main>
      {#if adminMode}
        {@render adminContent()}
      {:else}
      {#if detail}
        <button class="link" onclick={() => history.back()}>← Back</button>
        <div class="detail-body">
          <div class="detail-main">
            <div class="titlebar">
              <h2>
                {detail.title} <span class="year">({detail.year})</span>
                {#if detail.watched}<span class="watched-badge">✓ Watched</span>{/if}
              </h2>
              {#if !playing}
                <div class="title-actions">
                  <button
                    class="heart"
                    class:on={detail.favorite}
                    title={detail.favorite ? 'Remove from favorites' : 'Add to favorites'}
                    onclick={toggleFavorite}
                  >
                    <Icon icon={detail.favorite ? faHeartSolid : faHeartOutline} />
                  </button>
                  <button class="play" onclick={() => playFile(currentFile)}>▶ {hasResume ? 'Continue' : 'Play'}</button>
                </div>
              {/if}
            </div>

            {#if playing}
              <video class="player" controls autoplay bind:this={videoEl}></video>
            {/if}

            {#if detail.description}<p>{detail.description}</p>{/if}
            {#if detail.tags.length}<p class="tags">{#each detail.tags as t}<span>{t}</span>{/each}</p>{/if}

            {#if detail.files.length > 1}
              <h3>Episodes</h3>
              {#if seasons.length > 1}
                <div class="seasons">
                  {#each seasons as s}
                    <button class:active={s.season === currentSeason} class:watched={s.watched} onclick={() => (currentSeason = s.season)}>
                      {s.season ? 'Season ' + s.season : 'Episodes'}
                    </button>
                  {/each}
                </div>
              {/if}
              <div class="episodes">
                {#each currentEpisodes as f}
                  <button class:active={f.index === currentFile} class:watched={f.watched} onclick={() => playFile(f.index)} title={f.name}>
                    {episodeLabel(f)}
                  </button>
                {/each}
              </div>
            {/if}

            {#if detail.metadata.length}
              <table>
                <tbody>
                  {#each detail.metadata as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
                </tbody>
              </table>
            {/if}

            {#if detail.ratings.length}
              <h3>Ratings</h3>
              <table>
                <tbody>
                  {#each detail.ratings as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
                </tbody>
              </table>
            {/if}

            {#if detail.actors.length}
              <h3>Cast</h3>
              <ul>{#each detail.actors as a}<li>{a}</li>{/each}</ul>
            {/if}

            {#if detail.plot}<h3>Plot</h3><p>{detail.plot}</p>{/if}
          </div>

          {#if detail.hasPoster}
            <aside class="detail-poster">
              <img src={'/api/media/' + detail.id + '/poster'} alt={detail.title} />
            </aside>
          {/if}
        </div>
      {:else if activeCat}
        <div class="grid">
          {#each mediaList as m}{@render tile(m, null, true)}{/each}
        </div>
      {:else}
        <h2>Continue watching</h2>
        {#if continueList.length}
          <div class="grid">
            {#each continueList as m}{@render tile(m, removeFromContinue, false)}{/each}
          </div>
        {:else}
          <p class="center">Nothing in progress - pick a category to start watching.</p>
        {/if}
        {#if favoritesList.length}
          <h2>Favorites</h2>
          <div class="grid">
            {#each favoritesList as m}{@render tile(m, removeFromFavorites, false)}{/each}
          </div>
        {/if}
        {#if completedList.length}
          <h2>Completed</h2>
          <div class="grid">
            {#each completedList as m}{@render tile(m, removeFromCompleted, false)}{/each}
          </div>
        {/if}
      {/if}
      {/if}
    </main>
  </div>
{/if}

{#snippet tile(m, onRemove, showWatched)}
  <div class="tile">
    <button class="card" onclick={() => openMedia(m)}>
      <div class="poster">
        {#if m.hasPoster}
          <img src={'/api/media/' + m.id + '/poster'} alt={m.title} />
        {:else}
          <div class="noposter">{m.title}</div>
        {/if}
        {#if showWatched && m.watched}
          <span class="card-watched" title="Watched"><Icon icon={faCheck} /></span>
        {/if}
      </div>
      <span>{m.title}</span>
      <span class="year">{m.year}</span>
    </button>
    {#if onRemove}
      <button class="tile-remove" title="Remove" onclick={() => onRemove(m)}><Icon icon={faXmark} /></button>
    {/if}
  </div>
{/snippet}

{#snippet adminContent()}
  {#if !adminData}
    <p class="center">Loading…</p>
  {:else if adminPage === 'optimizer'}
    <h2>Optimizer queue</h2>
    {#if !adminData.enabled}
      <p class="center">The optimizer is disabled.</p>
    {:else if !adminData.items.length}
      <p class="center">Queue empty - everything is optimized.</p>
    {:else}
      <table class="admin-table">
        <thead><tr><th>State</th><th>Source</th></tr></thead>
        <tbody>
          {#each adminData.items as it}
            <tr>
              <td><span class="state state-{it.state}">{it.state}</span></td>
              <td>{it.source}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {:else if adminPage === 'users'}
    <h2>Users</h2>
    <div class="cards">
      {#each adminData as u}
        <div class="user-tile">
          <div class="user-name">{u.user}{#if u.admin}<span class="admin-tag">admin</span>{/if}</div>
          <div class="user-stats">
            <span><strong>{u.completed}</strong> completed</span>
            <span><strong>{u.favorites}</strong> favorites</span>
          </div>
        </div>
      {/each}
    </div>
  {:else}
    <h2>Dashboard</h2>
    <div class="cards">
      <div class="stat"><span class="num">{adminData.library.categories}</span><span class="cap">Categories</span></div>
      <div class="stat"><span class="num">{adminData.library.media}</span><span class="cap">Media items</span></div>
      <div class="stat"><span class="num">{adminData.library.files}</span><span class="cap">Files</span></div>
      <div class="stat"><span class="num">{adminData.users.total}</span><span class="cap">Users ({adminData.users.admins} admin)</span></div>
    </div>
    <h3>Optimizer</h3>
    <table>
      <tbody>
        <tr><th>Enabled</th><td>{adminData.optimizer.enabled ? 'yes' : 'no'}</td></tr>
        <tr><th>Max workers</th><td>{adminData.optimizer.maxWorkers || 'auto'}</td></tr>
        <tr><th>Pending</th><td>{adminData.optimizer.pending}</td></tr>
        <tr><th>Active</th><td>{adminData.optimizer.active}</td></tr>
      </tbody>
    </table>
  {/if}
{/snippet}
