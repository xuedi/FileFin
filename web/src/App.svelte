<script>
  import { onMount } from 'svelte'

  let booting = $state(true)
  let needsSetup = $state(false)
  let me = $state(null) // { user, admin }
  let view = $state('library') // 'library' | 'admin'
  let adminView = $state('dashboard') // 'dashboard' | 'library' | 'users' | 'settings' | 'progress'

  // install form
  let iuser = $state('')
  let ipass = $state('')
  let iport = $state(8080)
  let installError = $state('')


  // login form
  let luser = $state('')
  let lpass = $state('')
  let loginError = $state('')

  async function api(path, opts) {
    const res = await fetch(path, opts)
    if (!res.ok) throw res
    const ct = res.headers.get('content-type') || ''
    return ct.includes('application/json') ? res.json() : null
  }

  onMount(async () => {
    window.addEventListener('popstate', route) // browser back/forward restores the view
    // A tab/page close cannot await a fetch, so flush the last position via sendBeacon.
    const flush = () => {
      if (playing && detail) reportProgress(detail.id, currentFile, 'pagehide', true)
    }
    window.addEventListener('pagehide', flush)
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'hidden') flush()
    })
    try {
      const st = await api('/api/state')
      needsSetup = st.needsSetup
      if (needsSetup) {
        try {
          const r = await api('/api/install/browse') // defaults to the app's current directory
          dataDir = r.path
        } catch {
          dataDir = ''
        }
      } else {
        try {
          me = await api('/api/me')
          if (me?.admin) await loadSettings() // mediaFormat gates the Library import UI
          await route() // restore the view from the current URL (refresh / deep link)
        } catch {
          me = null
        }
      }
    } catch (e) {
      console.error(e)
    } finally {
      booting = false
    }
  })

  // data folder picker (install form)
  let dataDir = $state('')
  let browseOpen = $state(false)
  let browsePath = $state('')
  let browseParent = $state('')
  let browseEntries = $state([])
  let browseError = $state('')

  async function openBrowser() {
    browseOpen = true
    await navigate('') // empty path: server starts at the app's current directory
  }

  async function navigate(path) {
    browseError = ''
    try {
      const q = path ? '?path=' + encodeURIComponent(path) : ''
      const r = await api('/api/install/browse' + q)
      browsePath = r.path
      browseParent = r.parent
      browseEntries = r.entries
    } catch {
      browseError = 'Cannot open that folder'
    }
  }

  function selectFolder() {
    dataDir = browsePath
    browseOpen = false
  }

  async function doInstall(e) {
    e.preventDefault()
    installError = ''
    try {
      const r = await api('/api/install', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username: iuser, password: ipass, port: Number(iport), dataDir }),
      })
      // The server is rebinding to the chosen port; reload the page there.
      const url = window.location.protocol + '//' + window.location.hostname + ':' + r.port + '/'
      setTimeout(() => {
        window.location.href = url
      }, 800)
    } catch {
      installError = 'Setup failed'
    }
  }

  async function doLogin(e) {
    e.preventDefault()
    loginError = ''
    try {
      me = await api('/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username: luser, password: lpass }),
      })
      if (me?.admin) await loadSettings()
      await route() // honor the URL the login page sat on (deep link / refresh)
    } catch {
      loginError = 'Invalid credentials'
    }
  }

  async function signOut() {
    try {
      await api('/api/logout', { method: 'POST' })
    } catch {}
    me = null
    luser = ''
    lpass = ''
    view = 'library'
    adminView = 'dashboard'
    homeCategory = ''
    libMode = 'home'
    detail = null
    playing = false
    history.replaceState({}, '', '/')
  }

  // end-user home: category aliases shown as nav links under Home
  let homeCategories = $state([])
  let homeCategory = $state('') // selected category name, '' = Home

  async function loadHomeCategories() {
    try {
      homeCategories = await api('/api/categories')
    } catch {
      homeCategories = []
    }
  }

  function homeCategoryAlias(name) {
    return homeCategories.find((c) => c.name === name)?.alias ?? name
  }

  // --- library views: home grids, category grid, media detail + player ---
  let libMode = $state('home') // 'home' | 'category' | 'detail'
  let homeData = $state({ continue: [], favorites: [], completed: [] })
  let categoryMedia = $state([])
  let detail = $state(null)
  let currentFile = $state(0)
  let currentSeason = $state(null)
  let playing = $state(false)
  let videoEl = $state(null)
  let hls = null
  let pendingSeek = 0

  const seasons = $derived(groupSeasons(detail))
  const currentEpisodes = $derived(seasons.find((s) => s.season === currentSeason)?.episodes ?? [])
  // "Continue" rather than "Play" when there is an unfinished resume point.
  const hasResume = $derived(!!detail && !detail.watched && (detail.continueIndex > 0 || detail.continueSeconds > 0))

  async function loadHome() {
    try {
      homeData = await api('/api/home')
    } catch {
      homeData = { continue: [], favorites: [], completed: [] }
    }
  }

  async function loadCategoryMedia(id) {
    try {
      categoryMedia = await api('/api/category/' + id + '/media')
    } catch {
      categoryMedia = []
    }
  }

  async function showMedia(id) {
    playing = false
    detail = await api('/api/media/' + id)
    // Preselect the furthest reached file (Continue); a fully watched folder starts over.
    const start = detail.watched ? 0 : detail.continueIndex || 0
    currentFile = detail.files[start]?.index ?? 0
    const groups = groupSeasons(detail)
    currentSeason = seasonOfFile(groups, currentFile, groups.length ? groups[0].season : null)
  }

  function openMedia(m) {
    go('/media/' + m.id)
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

  function seasonOfFile(groups, idx, fallback) {
    for (const g of groups) {
      if (g.episodes.some((e) => e.index === idx)) return g.season
    }
    return fallback
  }

  // Short chip label for an episode: "E1" when numbered, else the file name.
  function episodeLabel(f) {
    return f.episode ? 'E' + f.episode : f.name
  }

  function playFile(idx) {
    // Resume to the saved second only when starting the furthest-unfinished file; an
    // explicit pick of any other file starts it from the beginning.
    const resumeIdx = detail && !detail.watched ? detail.continueIndex || 0 : -1
    pendingSeek = idx === (detail?.files[resumeIdx]?.index ?? -1) ? detail.continueSeconds || 0 : 0
    currentFile = idx
    playing = true
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

  // Home tile "x": remove from a row by clearing the matching status.
  async function removeFromContinue(m) {
    homeData.continue = homeData.continue.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/progress', { method: 'DELETE' }).catch(() => {})
  }
  async function removeFromFavorites(m) {
    homeData.favorites = homeData.favorites.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/favorite', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ favorite: false }),
    }).catch(() => {})
  }
  async function removeFromCompleted(m) {
    homeData.completed = homeData.completed.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/watched', { method: 'DELETE' }).catch(() => {})
  }

  // Best-effort progress report; failures are ignored. sendBeacon is used for page/tab
  // teardown where a normal fetch may be cancelled.
  function reportProgress(mediaId, file, event, useBeacon = false, el = videoEl) {
    if (!el) return
    const position = el.currentTime
    const duration = el.duration
    if (!duration || !isFinite(duration)) return
    const body = JSON.stringify({ file, position, duration, event })
    const url = '/api/media/' + mediaId + '/progress'
    if (useBeacon && navigator.sendBeacon) {
      navigator.sendBeacon(url, new Blob([body], { type: 'application/json' }))
      return
    }
    api(url, { method: 'POST', headers: { 'content-type': 'application/json' }, body }).catch(() => {})
  }

  // Wire up playback whenever the player appears or the chosen file changes. Direct-play
  // files get a plain src; transcoded files load HLS via the browser's native HLS on
  // Safari, or hls.js (lazily imported, bundled by Vite) elsewhere.
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
      track.src = base + '/sub/' + sub.index
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

  // --- TokTok mode: binge-play every video of the current category back to back ---
  let tokOn = $state(false)
  let tokVideoEl = $state(null)
  let tokMediaIdx = $state(0) // index into categoryMedia
  let tokFiles = $state([]) // files of the current media item, in play order
  let tokFileIdx = $state(0) // index into tokFiles
  let tokMediaId = $state('')
  let tokTitle = $state('')
  let tokCurrent = $state(null) // { mediaId, file, transcode, subtitles }
  let tokHls = null

  async function startTokTok() {
    if (!categoryMedia.length || tokOn) return
    tokOn = true
    await loadTokMedia(0)
  }

  function stopTokTok() {
    tokOn = false
    tokCurrent = null
    if (tokHls) {
      tokHls.destroy()
      tokHls = null
    }
    if (document.fullscreenElement) document.exitFullscreen?.().catch(() => {})
  }

  // loadTokMedia fetches one category item's files and starts playing, skipping any item
  // that fails to load or has no files. Going forward it starts the first file and stops
  // past the end of the category; going back (fromEnd) it starts the last file and ignores
  // stepping before the very first video.
  async function loadTokMedia(idx, fromEnd = false) {
    if (idx < 0) return // already at the first video: nothing earlier to play
    if (idx >= categoryMedia.length) {
      stopTokTok()
      return
    }
    try {
      const d = await api('/api/media/' + categoryMedia[idx].id)
      const files = (d.files || [])
        .slice()
        .sort((a, b) => (a.season || 0) - (b.season || 0) || (a.episode || 0) - (b.episode || 0) || a.index - b.index)
      if (!files.length) {
        await loadTokMedia(fromEnd ? idx - 1 : idx + 1, fromEnd)
        return
      }
      tokMediaIdx = idx
      tokMediaId = d.id
      tokTitle = d.title
      tokFiles = files
      playTokFile(fromEnd ? files.length - 1 : 0)
    } catch {
      await loadTokMedia(fromEnd ? idx - 1 : idx + 1, fromEnd)
    }
  }

  function playTokFile(i) {
    const f = tokFiles[i]
    if (!f) {
      advanceTok()
      return
    }
    tokFileIdx = i
    tokCurrent = { mediaId: tokMediaId, file: f.index, transcode: f.transcode, subtitles: f.subtitles || [] }
  }

  // advanceTok plays the next file of the current item, else the next category item.
  function advanceTok() {
    if (tokFileIdx + 1 < tokFiles.length) {
      playTokFile(tokFileIdx + 1)
    } else {
      loadTokMedia(tokMediaIdx + 1)
    }
  }

  // previousTok is the inverse: the previous file of the current item, else the last file
  // of the previous category item.
  function previousTok() {
    if (tokFileIdx > 0) {
      playTokFile(tokFileIdx - 1)
    } else {
      loadTokMedia(tokMediaIdx - 1, true)
    }
  }

  function tokKeydown(e) {
    if (!tokOn) return
    if (e.key === 'Escape') {
      stopTokTok()
    } else if (e.key === 'ArrowRight') {
      e.preventDefault() // don't let the native player seek instead
      advanceTok()
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      previousTok()
    }
  }

  // Wire the TokTok player whenever the current item changes (same direct-play vs HLS
  // decision as the detail player); on 'ended' it auto-advances to the next video.
  $effect(() => {
    if (!tokOn || !tokVideoEl || !tokCurrent) return
    const { mediaId, file, transcode, subtitles } = tokCurrent
    const base = '/api/media/' + mediaId + '/file/' + file
    let cancelled = false
    if (!transcode) {
      tokVideoEl.src = base
    } else {
      const url = base + '/hls/index.m3u8'
      if (tokVideoEl.canPlayType('application/vnd.apple.mpegurl')) {
        tokVideoEl.src = url
      } else {
        import('hls.js').then(({ default: Hls }) => {
          if (cancelled || !tokVideoEl) return
          if (Hls.isSupported()) {
            tokHls = new Hls()
            tokHls.loadSource(url)
            tokHls.attachMedia(tokVideoEl)
          } else {
            tokVideoEl.src = url
          }
        })
      }
    }
    tokVideoEl.querySelectorAll('track').forEach((t) => t.remove())
    for (const sub of subtitles) {
      const track = document.createElement('track')
      track.kind = 'subtitles'
      track.srclang = sub.lang
      track.label = sub.label || sub.lang
      track.src = base + '/sub/' + sub.index
      tokVideoEl.appendChild(track)
    }
    tokVideoEl.play?.().catch(() => {})

    let lastMark = 0
    const onTime = () => {
      if (tokVideoEl && Math.abs(tokVideoEl.currentTime - lastMark) >= 30) {
        lastMark = tokVideoEl.currentTime
        reportProgress(mediaId, file, 'checkpoint', false, tokVideoEl)
      }
    }
    const onEnded = () => {
      reportProgress(mediaId, file, 'ended', false, tokVideoEl)
      advanceTok()
    }
    tokVideoEl.addEventListener('timeupdate', onTime)
    tokVideoEl.addEventListener('ended', onEnded)
    return () => {
      cancelled = true
      if (tokVideoEl) {
        tokVideoEl.removeEventListener('timeupdate', onTime)
        tokVideoEl.removeEventListener('ended', onEnded)
        tokVideoEl.querySelectorAll('track').forEach((t) => t.remove())
      }
      if (tokHls) {
        tokHls.destroy()
        tokHls = null
      }
    }
  })

  // --- client routing (History API): the URL reflects the view so refresh stays put ---
  // go() pushes a URL then applies it; route() applies the current URL without pushing.
  function go(path) {
    history.pushState({}, '', path)
    route()
  }

  async function route() {
    const segs = location.pathname.split('/').filter(Boolean)
    if (segs[0] === 'admin' && me?.admin) {
      view = 'admin'
      const page = ['dashboard', 'library', 'users', 'settings', 'progress'].includes(segs[1]) ? segs[1] : 'dashboard'
      applyAdmin(page, segs[2])
      return
    }
    // Library (also the fallback for non-admins hitting /admin).
    view = 'library'
    await loadHomeCategories()
    playing = false // a route change tears the player down (its effect cleanup reports a stop)
    if (segs[0] === 'media' && segs[1]) {
      libMode = 'detail'
      await showMedia(segs[1])
    } else if (segs[0] === 'category' && segs[1]) {
      const c = homeCategories.find((x) => String(x.id) === segs[1])
      homeCategory = c ? c.name : ''
      libMode = 'category'
      detail = null
      await loadCategoryMedia(segs[1])
    } else {
      homeCategory = ''
      libMode = 'home'
      detail = null
      await loadHome()
    }
  }

  // applyAdmin sets the admin sub-view and loads its data, without touching history.
  // sub is the optional third path segment ("import" resumes a prepared import).
  function applyAdmin(page, sub) {
    if (page !== 'progress') stopProgressPoll() // leaving Progress stops its poller
    adminView = page
    if (page === 'library') {
      clearInterval(plexTimer) // stop any orphaned Plex staging poll
      plexTimer = 0
      clearInterval(jellyfinTimer) // stop any orphaned Jellyfin staging poll
      jellyfinTimer = 0
      importPage = ''
      editName = ''
      loadCategories()
      loadPendingImports().then(() => {
        if (sub === 'import' && pendingImports.length > 0) continueImport()
      })
    } else if (page === 'settings') {
      formatChoice = ''
      settingsError = ''
      rebuildMsg = ''
      ifBrowseOpen = false
      editOmdb = false
      editLogging = false
      editTranscoding = false
      editSubtitle = false
      loadSettings()
    } else if (page === 'users') {
      editUserId = 0
      loadUsers()
    } else if (page === 'dashboard') {
      summary = null
      loadSummary()
    } else if (page === 'progress') {
      startProgressPoll()
    }
  }

  function showLibrary() {
    go('/')
  }

  function goAdmin() {
    go('/admin/' + adminView)
  }

  // admin library (category management)
  let categories = $state([])
  let catName = $state('')
  let catAlias = $state('')
  let catParentId = $state(0) // 0 = create a top-level category
  let catError = $state('')

  // treeOrder flattens a flat category list (each carrying parentId) into display order -
  // every parent immediately followed by its sub-categories, recursively - annotating each
  // row with a `_depth` for indentation. Routing stays by id, so this only affects display.
  function treeOrder(cats) {
    const byParent = new Map()
    for (const c of cats) {
      const p = c.parentId || 0
      if (!byParent.has(p)) byParent.set(p, [])
      byParent.get(p).push(c)
    }
    for (const list of byParent.values()) list.sort((a, b) => (a.leaf ?? a.name).localeCompare(b.leaf ?? b.name))
    const out = []
    const walk = (parentId, depth) => {
      for (const c of byParent.get(parentId) ?? []) {
        out.push({ ...c, _depth: depth })
        walk(c.id, depth + 1)
      }
    }
    walk(0, 0)
    return out
  }

  // treeMarker is the indentation + box-drawing branch prefix for a sub-category at depth.
  function treeMarker(depth) {
    if (!depth || depth <= 0) return ''
    return '  '.repeat(depth - 1) + '└─ '
  }

  let categoryTree = $derived(treeOrder(categories))
  let homeTree = $derived(treeOrder(homeCategories))

  async function loadCategories() {
    catError = ''
    try {
      categories = await api('/api/admin/categories')
      if (!categories.some((c) => c.name === importCategory)) {
        importCategory = categories[0]?.name ?? ''
      }
    } catch {
      catError = 'Could not load categories'
    }
  }

  function openAdminLibrary() {
    go('/admin/library')
  }

  // admin settings (media-format gate + read-only config list)
  let mediaFormat = $state('') // shared: '' until permanently chosen
  let importFolder = $state('') // shared: configured import source path, '' = not set
  let omdbKey = $state('') // shared: OMDb API key, '' = not set
  let logLevel = $state('info') // shared: error|info|debug
  let logOutput = $state('STDOUT') // shared: STDOUT|STDERR|file path
  let transcodeEnabled = $state(true) // shared: on/off
  let ffmpegPath = $state('ffmpeg') // shared
  let ffprobePath = $state('ffprobe') // shared
  let subtitleLanguage = $state('en') // shared
  let optimizeMode = $state('none') // shared: none|cpu|gpu|all
  let settings = $state([]) // [{name, value}]
  let formatChoice = $state('') // selected box in the gate
  let settingsError = $state('')

  // inline OMDb-key editing (same pattern as the category alias edit)
  let editOmdb = $state(false)
  let omdbInput = $state('')

  function startEditOmdb() {
    omdbInput = omdbKey
    editOmdb = true
    settingsError = ''
  }

  async function saveOmdbKey() {
    settingsError = ''
    try {
      const r = await api('/api/admin/settings/omdb-key', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ key: omdbInput.trim() }),
      })
      omdbKey = r.omdbKey
      settings = r.settings
      editOmdb = false
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not save the OMDb key'
    }
  }

  // inline logging edit (level dropdown + output text, saved together)
  let editLogging = $state(false)
  let logLevelInput = $state('info')
  let logOutputInput = $state('STDOUT')

  function startEditLogging() {
    logLevelInput = logLevel
    logOutputInput = logOutput
    editLogging = true
    settingsError = ''
  }

  async function saveLogging() {
    settingsError = ''
    try {
      const r = await api('/api/admin/settings/logging', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ level: logLevelInput, output: logOutputInput.trim() }),
      })
      logLevel = r.logLevel
      logOutput = r.logOutput
      settings = r.settings
      editLogging = false
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not save logging settings'
    }
  }

  // inline transcoding edit (ffmpeg/ffprobe paths + on/off toggle, saved together)
  let editTranscoding = $state(false)
  let transcodeEnabledInput = $state(true)
  let ffmpegPathInput = $state('ffmpeg')
  let ffprobePathInput = $state('ffprobe')

  function startEditTranscoding() {
    transcodeEnabledInput = transcodeEnabled
    ffmpegPathInput = ffmpegPath
    ffprobePathInput = ffprobePath
    editTranscoding = true
    settingsError = ''
  }

  async function saveTranscoding() {
    settingsError = ''
    try {
      const r = await api('/api/admin/settings/transcoding', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          ffmpegPath: ffmpegPathInput.trim(),
          ffprobePath: ffprobePathInput.trim(),
          enabled: transcodeEnabledInput,
        }),
      })
      transcodeEnabled = r.transcodeEnabled
      ffmpegPath = r.ffmpegPath
      ffprobePath = r.ffprobePath
      settings = r.settings
      editTranscoding = false
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not save transcoding settings'
    }
  }

  // inline subtitle-language edit
  let editSubtitle = $state(false)
  let subtitleInput = $state('en')

  function startEditSubtitle() {
    subtitleInput = subtitleLanguage
    editSubtitle = true
    settingsError = ''
  }

  async function saveSubtitle() {
    settingsError = ''
    try {
      const r = await api('/api/admin/settings/subtitle-language', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ language: subtitleInput.trim() }),
      })
      subtitleLanguage = r.subtitleLanguage
      settings = r.settings
      editSubtitle = false
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not save the subtitle language'
    }
  }

  // inline optimizer-mode edit (editable dropdown, same pattern as the log-level select)
  const optimizeModes = [
    { value: 'none', label: 'NONE' },
    { value: 'cpu', label: 'CPU only' },
    { value: 'gpu', label: 'GPU only' },
    { value: 'all', label: 'ALL' },
  ]
  let editOptimizer = $state(false)
  let optimizeModeInput = $state('none')

  function startEditOptimizer() {
    optimizeModeInput = optimizeMode
    editOptimizer = true
    settingsError = ''
  }

  async function saveOptimizer() {
    settingsError = ''
    try {
      const r = await api('/api/admin/settings/optimizer', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ mode: optimizeModeInput }),
      })
      optimizeMode = r.optimizeMode
      settings = r.settings
      editOptimizer = false
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not save the optimizer mode'
    }
  }

  // import-folder picker (settings) - the installer's folder browser against admin browse
  let ifBrowseOpen = $state(false)
  let ifPath = $state('')
  let ifParent = $state('')
  let ifEntries = $state([])
  let ifError = $state('')

  async function openImportFolderBrowser() {
    ifBrowseOpen = true
    await importFolderNavigate(importFolder || '')
  }

  async function importFolderNavigate(path) {
    ifError = ''
    try {
      const r = await api('/api/admin/browse' + (path ? '?path=' + encodeURIComponent(path) : ''))
      ifPath = r.path
      ifParent = r.parent
      ifEntries = r.entries
    } catch {
      ifError = 'Cannot open that folder'
    }
  }

  async function selectImportFolder() {
    settingsError = ''
    try {
      const r = await api('/api/admin/settings/import-folder', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ path: ifPath }),
      })
      importFolder = r.importFolder
      settings = r.settings
      ifBrowseOpen = false
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not save the import folder'
    }
  }

  const formatBoxes = [
    {
      id: 'filefin',
      title: 'FileFin',
      desc: 'Year first, in parentheses.',
      movie: '(1999) The Matrix/(1999) The Matrix.mkv',
      episode: '(2002) Firefly/(2002) Firefly - 1x1.mkv',
    },
    {
      id: 'jellyfin',
      title: 'Jellyfin',
      desc: 'Year last; episodes as SxxEyy.',
      movie: 'The Matrix (1999)/The Matrix (1999).mkv',
      episode: 'Firefly (2002)/Firefly (2002) S01E01.mkv',
    },
    {
      id: 'plex',
      title: 'Plex',
      desc: 'Year last; episodes as sNNeNN.',
      movie: 'The Matrix (1999)/The Matrix (1999).mkv',
      episode: 'Firefly (2002)/Firefly (2002) - s01e01.mkv',
    },
  ]

  async function loadSettings() {
    try {
      const r = await api('/api/admin/settings')
      mediaFormat = r.mediaFormat
      importFolder = r.importFolder
      omdbKey = r.omdbKey
      logLevel = r.logLevel
      logOutput = r.logOutput
      transcodeEnabled = r.transcodeEnabled
      ffmpegPath = r.ffmpegPath
      ffprobePath = r.ffprobePath
      subtitleLanguage = r.subtitleLanguage
      optimizeMode = r.optimizeMode
      settings = r.settings
    } catch {}
  }

  function openSettings() {
    go('/admin/settings')
  }

  // rebuild: flush the cache and rebuild it from the data folder
  let rebuilding = $state(false)
  let rebuildMsg = $state('')

  async function rebuildDb() {
    if (!confirm('Flush the cache and rebuild it from the data folder? This also clears any pending imports.')) return
    rebuilding = true
    rebuildMsg = ''
    settingsError = ''
    try {
      const r = await api('/api/admin/rebuild', { method: 'POST' })
      rebuildMsg = `Rebuilt ${r.categories} categor${r.categories === 1 ? 'y' : 'ies'} and ${r.media} media item${r.media === 1 ? '' : 's'}.`
      await loadSettings()
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Rebuild failed'
    } finally {
      rebuilding = false
    }
  }

  // optimizer scan: fill the optimize queue from the cached media (manual, no auto-discovery)
  let optimizeScanning = $state(false)
  let optimizeScanMsg = $state('')

  async function optimizeScan() {
    optimizeScanning = true
    optimizeScanMsg = ''
    settingsError = ''
    try {
      const r = await api('/api/admin/optimize/scan', { method: 'POST' })
      optimizeScanMsg = `Found ${r.candidates} file${r.candidates === 1 ? '' : 's'} to optimize; ${r.pending} waiting in line.`
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Optimizer scan failed'
    } finally {
      optimizeScanning = false
    }
  }

  // OMDB enrichment scan: queue every un-enriched media folder; the background agent
  // works through the queue and fills metadata + posters.
  let enrichScanning = $state(false)
  let enrichScanMsg = $state('')

  async function enrichScan() {
    enrichScanning = true
    enrichScanMsg = ''
    settingsError = ''
    try {
      const r = await api('/api/admin/enrich/scan', { method: 'POST' })
      enrichScanMsg = `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for enrichment; ${r.pending} waiting in line.`
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'OMDB enrichment scan failed'
    } finally {
      enrichScanning = false
    }
  }

  let thumbnailScanning = $state(false)
  let thumbnailScanMsg = $state('')

  async function thumbnailScan() {
    thumbnailScanning = true
    thumbnailScanMsg = ''
    settingsError = ''
    try {
      const r = await api('/api/admin/thumbnail/scan', { method: 'POST' })
      thumbnailScanMsg = `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for thumbnails; ${r.pending} waiting in line.`
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Thumbnail scan failed'
    } finally {
      thumbnailScanning = false
    }
  }

  async function selectFormat() {
    if (!formatChoice) return
    settingsError = ''
    try {
      const r = await api('/api/admin/settings/format', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ format: formatChoice }),
      })
      mediaFormat = r.mediaFormat
      settings = r.settings
    } catch (e) {
      settingsError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not save the format'
    }
  }

  async function addCategory() {
    catError = ''
    try {
      await api('/api/admin/categories', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ name: catName.trim(), alias: catAlias.trim(), parentId: catParentId }),
      })
      catName = ''
      catAlias = ''
      catParentId = 0
      await loadCategories()
    } catch (e) {
      catError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not add category'
    }
  }

  // inline alias editing in the admin table
  let editName = $state('') // category being edited, '' = none
  let editAlias = $state('')

  function startEditAlias(c) {
    editName = c.name
    editAlias = c.alias
    catError = ''
  }

  async function saveAlias() {
    try {
      // Preserve the category's current other-media flag: the PUT carries both fields, so
      // omitting it would clear the flag whenever an alias is edited.
      const other = categories.find((c) => c.name === editName)?.otherMedia ?? false
      await api('/api/admin/categories/' + encodeURIComponent(editName), {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ alias: editAlias.trim(), otherMedia: other }),
      })
      editName = ''
      await loadCategories()
    } catch (e) {
      catError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not update alias'
    }
  }

  // toggleOtherMedia flips a category's other-media flag immediately (no Edit/Save), via
  // the same extended alias endpoint so alias and flag stay in one PUT.
  async function toggleOtherMedia(c, value) {
    try {
      await api('/api/admin/categories/' + encodeURIComponent(c.name), {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ alias: c.alias, otherMedia: value }),
      })
      await loadCategories()
    } catch (e) {
      catError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not update category'
    }
  }

  // admin import
  let importCategory = $state('')
  let importSource = $state('folder') // 'folder' (configured import folder) | 'upload' | 'plex'
  let importPage = $state('') // '' = config, else 'assess' | 'upload' | 'plex'

  // browser file selection (upload page)
  let uploadFiles = $state([])
  // one entry per picked file: { name, pct, status: 'pending'|'up'|'done'|'error' }
  let uploadProgress = $state([])
  let uploading = $state(false)
  let uploadError = $state('')
  // which source produced the current assessment, so the assess view can lock delete-after
  // on for uploads (their /tmp working files must always be cleaned up).
  let importOrigin = $state('folder') // 'folder' | 'upload' | 'plex'

  // assessment table (one row per video file found in the import folder)
  let assessRows = $state([])
  let assessLoading = $state(false)
  let assessError = $state('')
  // preCheck rows that survived a restart: an import prepared but not yet started.
  // Their presence lets the admin resume instead of starting a fresh assessment.
  let pendingImports = $state([])
  let editKey = $state('') // media group being edited, '' = none
  let editTitle = $state('')
  let editYear = $state('') // text input; validated as a number on save
  let editCategory = $state('') // category folder name chosen in the edit dropdown

  // The importer stages one row per file, but the assessment view is media-centric:
  // a show's episodes share one OMDb lookup, poster, and meta, so they collapse to a
  // single row keyed by (title, year) with a count of the contained media files.
  const assessGroups = $derived.by(() => {
    const map = new Map()
    for (const r of assessRows) {
      const key = (r.title || '') + ' ' + (r.year || 0)
      let g = map.get(key)
      if (!g) {
        g = { key, title: r.title, year: r.year, category: r.category, ids: [], count: 0, hasPoster: false, subCount: 0 }
        map.set(key, g)
      }
      g.ids.push(r.id)
      g.count++
      if (r.hasPoster) g.hasPoster = true
      g.subCount += r.subCount || 0
    }
    return [...map.values()]
  })
  // the import folder is a vacuum: default to clearing originals after a successful copy
  let deleteAfter = $state(true)

  function categoryAlias(name) {
    return categories.find((c) => c.name === name)?.alias ?? name
  }

  async function startImport() {
    uploadFiles = []
    uploadProgress = []
    uploadError = ''
    // The import working view is its own URL so a reload/back lands back here; push it
    // directly rather than via go() so route() does not reset to the resume path and
    // skip the fresh scan below.
    history.pushState({}, '', '/admin/library/import')
    if (importSource === 'upload') {
      importOrigin = 'upload'
      importPage = 'upload'
      return
    }
    if (importSource === 'plex') {
      importOrigin = 'plex'
      importPage = 'plex'
      await openPlexImport()
      return
    }
    if (importSource === 'jellyfin') {
      importOrigin = 'jellyfin'
      importPage = 'jellyfin'
      openJellyfinImport()
      return
    }
    importOrigin = 'folder'
    importPage = 'assess'
    await runAssess()
  }

  // loadPendingImports refreshes the set of staged-but-not-started (preCheck) rows.
  async function loadPendingImports() {
    try {
      pendingImports = await api('/api/admin/imports?status=preCheck')
    } catch {
      pendingImports = []
    }
  }

  // continueImport resumes a prepared import: it loads the existing preCheck rows
  // straight into the assessment table without re-scanning, so the earlier work
  // (titles, posters, subtitles) is preserved.
  function continueImport() {
    if (pendingImports.length === 0) return
    importCategory = pendingImports[0].category
    // Every row carries its producing source in `origin`, so a resumed import shows the
    // right assessment affordances (uploads lock cleanup on, Plex locks it off).
    importOrigin = pendingImports[0].origin || 'folder'
    if (importOrigin === 'upload') deleteAfter = true
    if (importOrigin === 'plex' || importOrigin === 'jellyfin') deleteAfter = false
    assessRows = pendingImports
    assessError = ''
    assessLoading = false
    importPage = 'assess'
  }

  async function runAssess() {
    assessError = ''
    assessLoading = true
    assessRows = []
    const cat = categories.find((c) => c.name === importCategory)
    try {
      assessRows = await api('/api/admin/import/assess', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ categoryId: cat?.id ?? 0 }),
      })
    } catch (e) {
      assessError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not assess the import folder'
    } finally {
      assessLoading = false
    }
  }

  function startEditImport(group) {
    editKey = group.key
    editTitle = group.title
    editYear = String(group.year || '')
    editCategory = group.category
  }

  // An edit re-titles and can re-categorise every file of the media (the whole show),
  // so all member rows update together and share the refreshed OMDb result.
  async function saveImportEdit() {
    const group = assessGroups.find((g) => g.key === editKey)
    if (!group) {
      editKey = ''
      return
    }
    const yearStr = editYear.trim()
    if (yearStr !== '' && !/^\d+$/.test(yearStr)) {
      assessError = 'Year must be a number'
      return
    }
    const year = yearStr === '' ? 0 : Number(yearStr)
    const title = editTitle.trim()
    const categoryId = categories.find((c) => c.name === editCategory)?.id ?? 0
    try {
      const updated = await Promise.all(
        group.ids.map((id) =>
          api('/api/admin/imports/' + id, {
            method: 'PUT',
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({ title, year, categoryId }),
          }),
        ),
      )
      const byId = new Map(updated.map((u) => [u.id, u]))
      assessRows = assessRows.map((r) => byId.get(r.id) || r)
      editKey = ''
      assessError = ''
    } catch (e) {
      assessError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not update the row'
    }
  }

  async function deleteImportRow(group) {
    try {
      await Promise.all(group.ids.map((id) => api('/api/admin/imports/' + id, { method: 'DELETE' })))
      const gone = new Set(group.ids)
      assessRows = assessRows.filter((r) => !gone.has(r.id))
    } catch {}
  }

  async function startImportBatch() {
    try {
      await api('/api/admin/import/start', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          deleteAfter:
            importOrigin === 'upload'
              ? true
              : importOrigin === 'plex' || importOrigin === 'jellyfin'
                ? false
                : deleteAfter,
        }),
      })
      importPage = ''
      pendingImports = []
      go('/admin/progress')
    } catch (e) {
      assessError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not start the import'
    }
  }

  function onUploadPick(e) {
    uploadFiles = [...e.target.files]
    uploadProgress = uploadFiles.map((f) => ({ name: f.name, pct: 0, status: 'pending' }))
    uploadError = ''
  }

  // uploadFile streams one file with progress; fetch cannot report upload progress, so this
  // uses XMLHttpRequest. onProgress receives an integer percent 0-100.
  function uploadFile(file, session, onProgress) {
    return new Promise((resolve, reject) => {
      const form = new FormData()
      form.append('session', session) // must precede the file part: the server reads it first
      form.append('file', file, file.name)
      const xhr = new XMLHttpRequest()
      xhr.open('POST', '/api/admin/import/upload/file')
      xhr.upload.onprogress = (ev) => {
        if (ev.lengthComputable) onProgress(Math.round((ev.loaded / ev.total) * 100))
      }
      xhr.onload = () =>
        xhr.status >= 200 && xhr.status < 300 ? resolve() : reject(new Error(xhr.responseText || 'upload failed'))
      xhr.onerror = () => reject(new Error('upload failed'))
      xhr.send(form)
    })
  }

  // startUpload opens a session, uploads every picked file (one progress bar each), then
  // stages them into the same assessment table the folder import uses.
  async function startUpload() {
    if (!uploadFiles.length || uploading) return
    uploading = true
    uploadError = ''
    try {
      const { session } = await api('/api/admin/import/upload/begin', { method: 'POST' })
      for (let i = 0; i < uploadFiles.length; i++) {
        uploadProgress[i].status = 'up'
        try {
          await uploadFile(uploadFiles[i], session, (pct) => (uploadProgress[i].pct = pct))
          uploadProgress[i].pct = 100
          uploadProgress[i].status = 'done'
        } catch (err) {
          uploadProgress[i].status = 'error'
          throw err
        }
      }
      const cat = categories.find((c) => c.name === importCategory)
      assessRows = await api('/api/admin/import/upload/assess', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ session, categoryId: cat?.id ?? 0 }),
      })
      deleteAfter = true
      importPage = 'assess'
    } catch (e) {
      uploadError = (e instanceof Response ? (await e.text()).trim() : e?.message) || 'Upload failed'
    } finally {
      uploading = false
    }
  }

  // --- admin import: Plex source ---
  let plexDB = $state('') // Plex database path
  let plexMetaDir = $state('') // optional metadata-dir override (advanced)
  let plexChecking = $state(false)
  let plexError = $state('')
  let plexChecked = $state(false) // a Check has populated the library list
  // one row per Plex library: { section, kind, count, selected, categoryId (0 = create),
  // searchBase, status, from, to, found, total, resolving }
  let plexRows = $state([])
  let plexStaging = $state(false)
  let plexProgress = $state(null) // { total, done, staged, missing, finished, error }
  let plexTimer = 0
  // Plex DB file browser (reuses the admin browse endpoint with files=true)
  let plexBrowseOpen = $state(false)
  let plexBrowse = $state({ path: '', parent: '', entries: [] })
  let plexBrowseError = $state('')

  // a library is ready to stage when it resolves green (paths located)
  const plexReady = $derived(
    plexRows.some((r) => r.selected) && plexRows.every((r) => !r.selected || r.status === 'green'),
  )

  async function openPlexImport() {
    plexError = ''
    plexChecked = false
    plexRows = []
    plexProgress = null
    plexStaging = false
    plexMetaDir = ''
    plexBrowseOpen = false
    try {
      const r = await api('/api/admin/import/plex/default')
      plexDB = r.dbPath || ''
    } catch {
      plexDB = ''
    }
  }

  async function plexCheck() {
    if (!plexDB.trim()) return
    plexError = ''
    plexChecking = true
    plexChecked = false
    try {
      const libs = await api('/api/admin/import/plex/check', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ dbPath: plexDB.trim(), metadataDir: plexMetaDir.trim() }),
      })
      plexRows = (libs || []).map((l) => ({
        section: l.name,
        kind: l.kind,
        count: l.count,
        selected: false,
        categoryId: 0, // 0 = create a category from the Plex library
        searchBase: '',
        status: '',
        from: '',
        to: '',
        found: 0,
        total: 0,
        resolving: false,
      }))
      plexChecked = true
    } catch (e) {
      plexError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not open the Plex database'
    } finally {
      plexChecking = false
    }
  }

  async function plexBrowseNavigate(path) {
    plexBrowseError = ''
    try {
      const q = '?files=true' + (path ? '&path=' + encodeURIComponent(path) : '')
      plexBrowse = await api('/api/admin/browse' + q)
    } catch {
      plexBrowseError = 'Cannot open that folder'
    }
  }

  async function openPlexBrowse() {
    plexBrowseOpen = true
    await plexBrowseNavigate('')
  }

  function pickPlexDB(entry) {
    if (entry.isDir) {
      plexBrowseNavigate(entry.path)
      return
    }
    plexDB = entry.path
    plexBrowseOpen = false
  }

  // togglePlexRow flips a library's selection; selecting one resolves its paths.
  async function togglePlexRow(row) {
    row.selected = !row.selected
    if (row.selected && row.status === '') await plexResolveRow(row)
  }

  // plexResolveRow re-checks one library's path status, applying its media-location
  // override when given.
  async function plexResolveRow(row) {
    row.resolving = true
    try {
      const res = await api('/api/admin/import/plex/resolve', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          dbPath: plexDB.trim(),
          sections: [row.section],
          searchBase: row.searchBase.trim(),
        }),
      })
      const r = res[0]
      if (r) {
        row.status = r.status
        row.from = r.from
        row.to = r.to
        row.found = r.found
        row.total = r.total
      }
    } catch (e) {
      plexError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not resolve paths'
    } finally {
      row.resolving = false
    }
  }

  async function startPlexStaging() {
    if (!plexReady || plexStaging) return
    const chosen = plexRows.filter((r) => r.selected)
    plexError = ''
    try {
      await api('/api/admin/import/plex/prepare', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          dbPath: plexDB.trim(),
          metadataDir: plexMetaDir.trim(),
          selections: chosen.map((r) => ({
            section: r.section,
            categoryId: r.categoryId,
            create: r.categoryId === 0,
          })),
          remaps: chosen.map((r) => ({ section: r.section, from: r.from, to: r.to })),
        }),
      })
      plexStaging = true
      plexProgress = { total: 0, done: 0, staged: 0, missing: 0, finished: false, error: '' }
      pollPlexProgress()
    } catch (e) {
      plexError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not start staging'
    }
  }

  function pollPlexProgress() {
    clearInterval(plexTimer)
    plexTimer = setInterval(async () => {
      try {
        const p = await api('/api/admin/import/plex/progress')
        plexProgress = p
        if (p.finished) {
          clearInterval(plexTimer)
          plexTimer = 0
          plexStaging = false
          if (p.error) {
            plexError = p.error
          } else {
            await loadPendingImports()
            go('/admin/library/import')
          }
        }
      } catch {
        clearInterval(plexTimer)
        plexTimer = 0
        plexStaging = false
      }
    }, 1000)
  }

  // --- admin import: Jellyfin (NFO) source ---
  let jellyfinDir = $state('') // source library directory on the server
  let jellyfinError = $state('')
  let jellyfinCategoryId = $state(0) // 0 = create a category from the typed name
  let jellyfinNewName = $state('') // folder name when creating a category
  let jellyfinStaging = $state(false)
  let jellyfinProgress = $state(null) // { total, done, staged, missing, finished, error }
  let jellyfinTimer = 0
  let jellyfinBrowseOpen = $state(false)
  let jellyfinBrowse = $state({ path: '', parent: '', entries: [] })
  let jellyfinBrowseError = $state('')

  const jellyfinReady = $derived(
    !!jellyfinDir.trim() && (jellyfinCategoryId !== 0 || !!jellyfinNewName.trim()),
  )

  function openJellyfinImport() {
    jellyfinDir = ''
    jellyfinError = ''
    jellyfinCategoryId = categories[0]?.id ?? 0
    jellyfinNewName = ''
    jellyfinProgress = null
    jellyfinStaging = false
    jellyfinBrowseOpen = false
  }

  async function jellyfinBrowseNavigate(path) {
    jellyfinBrowseError = ''
    try {
      const q = path ? '?path=' + encodeURIComponent(path) : ''
      jellyfinBrowse = await api('/api/admin/browse' + q)
    } catch {
      jellyfinBrowseError = 'Cannot open that folder'
    }
  }

  async function openJellyfinBrowse() {
    jellyfinBrowseOpen = true
    await jellyfinBrowseNavigate('')
  }

  function selectJellyfinDir() {
    jellyfinDir = jellyfinBrowse.path
    jellyfinBrowseOpen = false
  }

  async function startJellyfinStaging() {
    if (!jellyfinReady || jellyfinStaging) return
    jellyfinError = ''
    try {
      await api('/api/admin/import/jellyfin/prepare', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          sourceDir: jellyfinDir.trim(),
          categoryId: jellyfinCategoryId,
          create: jellyfinCategoryId === 0,
          category: jellyfinNewName.trim(),
        }),
      })
      jellyfinStaging = true
      jellyfinProgress = { total: 0, done: 0, staged: 0, missing: 0, finished: false, error: '' }
      pollJellyfinProgress()
    } catch (e) {
      jellyfinError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not start staging'
    }
  }

  function pollJellyfinProgress() {
    clearInterval(jellyfinTimer)
    jellyfinTimer = setInterval(async () => {
      try {
        const p = await api('/api/admin/import/jellyfin/progress')
        jellyfinProgress = p
        if (p.finished) {
          clearInterval(jellyfinTimer)
          jellyfinTimer = 0
          jellyfinStaging = false
          if (p.error) {
            jellyfinError = p.error
          } else {
            await loadPendingImports()
            go('/admin/library/import')
          }
        }
      } catch {
        clearInterval(jellyfinTimer)
        jellyfinTimer = 0
        jellyfinStaging = false
      }
    }, 1000)
  }

  // admin progress (live import copy bars + background optimizer encodes)
  let progressRows = $state([])
  // Like the optimizer/enricher, only the rows actually copying are shown; the queued
  // ones (status 'import', not yet picked up by the poller) collapse into a footnote.
  const importActive = $derived(progressRows.filter((r) => r.status === 'importing'))
  const importPending = $derived(progressRows.filter((r) => r.status === 'import').length)
  let optimizeRows = $state([]) // [{id,title,file,agent,percent}]
  let optimizePending = $state(0)
  let enrichRows = $state([]) // [{id,title,agent,status}]
  let enrichPending = $state(0)
  let thumbnailRows = $state([]) // [{id,title,agent,status}]
  let thumbnailPending = $state(0)
  let progressTimer = 0

  async function loadProgress() {
    try {
      progressRows = await api('/api/admin/imports/active')
    } catch {
      progressRows = []
    }
    try {
      const r = await api('/api/admin/optimize/active')
      optimizeRows = r.active
      optimizePending = r.pending
    } catch {
      optimizeRows = []
      optimizePending = 0
    }
    try {
      const r = await api('/api/admin/enrich/active')
      enrichRows = r.active
      enrichPending = r.pending
    } catch {
      enrichRows = []
      enrichPending = 0
    }
    try {
      const r = await api('/api/admin/thumbnail/active')
      thumbnailRows = r.active
      thumbnailPending = r.pending
    } catch {
      thumbnailRows = []
      thumbnailPending = 0
    }
  }

  function startProgressPoll() {
    loadProgress()
    stopProgressPoll()
    progressTimer = setInterval(loadProgress, 1000)
  }

  function stopProgressPoll() {
    if (progressTimer) {
      clearInterval(progressTimer)
      progressTimer = 0
    }
  }

  function pct(row) {
    if (!row.total) return 0
    return Math.min(100, Math.round((row.copied / row.total) * 100))
  }

  // admin dashboard
  let summary = $state(null)

  async function loadSummary() {
    try {
      summary = await api('/api/admin/summary')
    } catch {
      summary = null
    }
  }

  // admin users
  let users = $state([])
  let usersError = $state('')
  let newUserEmail = $state('')
  let newUserAlias = $state('')
  let newUserPassword = $state('')
  let newUserAdmin = $state(false)
  let editUserId = $state(0)
  let editUserAlias = $state('')

  const newUserReady = $derived(!!newUserEmail.trim() && !!newUserPassword)

  function fmtTime(ts) {
    if (!ts) return 'never'
    const d = new Date(ts * 1000)
    const p = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  }

  async function loadUsers() {
    usersError = ''
    try {
      users = await api('/api/admin/users')
    } catch (e) {
      users = []
      usersError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not load users'
    }
  }

  async function addUser() {
    usersError = ''
    try {
      await api('/api/admin/users', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          email: newUserEmail.trim(),
          alias: newUserAlias.trim(),
          password: newUserPassword,
          admin: newUserAdmin,
        }),
      })
      newUserEmail = ''
      newUserAlias = ''
      newUserPassword = ''
      newUserAdmin = false
      await loadUsers()
    } catch (e) {
      usersError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not create user'
    }
  }

  async function patchUser(u, body) {
    usersError = ''
    try {
      await api('/api/admin/users/' + u.id, {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      })
      await loadUsers()
    } catch (e) {
      usersError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not update user'
    }
  }

  function startEditUser(u) {
    editUserId = u.id
    editUserAlias = u.alias
  }

  async function saveUserAlias(u) {
    await patchUser(u, { alias: editUserAlias.trim() })
    editUserId = 0
  }

  async function deleteCategory(name) {
    if (!confirm(`Delete the empty category "${name}"?`)) return
    catError = ''
    try {
      await api('/api/admin/categories/' + encodeURIComponent(name), { method: 'DELETE' })
      await loadCategories()
    } catch (e) {
      catError = (e instanceof Response ? (await e.text()).trim() : '') || 'Could not delete category'
    }
  }
</script>

{#if booting}
  <p class="center">Loading...</p>
{:else if needsSetup}
  <form class="login" onsubmit={doInstall}>
    <h1>FileFin Setup</h1>
    <input placeholder="Admin username" bind:value={iuser} autocomplete="username" />
    <input type="password" placeholder="Password" bind:value={ipass} autocomplete="new-password" />
    <input type="number" placeholder="Server port" bind:value={iport} min="1" max="65535" />

    <label class="field-label">Data folder</label>
    <div class="data-folder">
      <input class="data-path" placeholder="No folder selected" bind:value={dataDir} readonly />
      <button type="button" onclick={openBrowser}>Browse</button>
    </div>
    {#if browseOpen}
      <div class="browser">
        <div class="browser-path">{browsePath}</div>
        {#if browseError}<p class="error">{browseError}</p>{/if}
        <ul class="browser-list">
          {#if browseParent}
            <li><button type="button" onclick={() => navigate(browseParent)}>.. (up)</button></li>
          {/if}
          {#each browseEntries as e}
            <li><button type="button" onclick={() => navigate(e.path)}>{e.name}</button></li>
          {/each}
        </ul>
        <button type="button" class="browser-select" onclick={selectFolder}>Select this folder</button>
      </div>
    {/if}

    <button type="submit" disabled={!dataDir}>Set up</button>
    {#if installError}<p class="error">{installError}</p>{/if}
  </form>
{:else if !me}
  <form class="login" onsubmit={doLogin}>
    <h1>FileFin</h1>
    <input placeholder="Username" bind:value={luser} autocomplete="username" />
    <input type="password" placeholder="Password" bind:value={lpass} autocomplete="current-password" />
    <button type="submit">Log in</button>
    {#if loginError}<p class="error">{loginError}</p>{/if}
  </form>
{:else}
  <header>
    <strong>FileFin</strong>
    <div class="header-actions">
      {#if me.admin}
        <div class="toggle">
          <button class:active={view === 'library'} onclick={showLibrary}>Library</button>
          <button class:active={view === 'admin'} onclick={goAdmin}>Admin</button>
        </div>
      {/if}
      <button class="link" onclick={signOut}>Sign out</button>
    </div>
  </header>
  <div class="layout">
    <nav>
      {#if view === 'library'}
        <button class:active={homeCategory === ''} onclick={() => go('/')}>Home</button>
        {#each homeTree as c}
          <button class:active={homeCategory === c.name} onclick={() => go('/category/' + c.id)}>{treeMarker(c._depth)}{c.alias}</button>
        {/each}
      {:else}
        <button class:active={adminView === 'dashboard'} onclick={() => go('/admin/dashboard')}>Dashboard</button>
        <button class:active={adminView === 'library'} onclick={openAdminLibrary}>Library</button>
        <button class:active={adminView === 'users'} onclick={() => go('/admin/users')}>Users</button>
        <button class:active={adminView === 'settings'} onclick={openSettings}>Settings</button>
        <button class:active={adminView === 'progress'} onclick={() => go('/admin/progress')}>Progress</button>
      {/if}
    </nav>
    <main>
      {#if view === 'library'}
        {#if libMode === 'detail'}
          {#if detail}
            <button class="link" onclick={() => history.back()}>&larr; Back</button>
            <div class="detail-body">
              <div class="detail-main">
                <div class="titlebar">
                  <h2>
                    {detail.title} <span class="year">({detail.year})</span>
                    {#if detail.watched}<span class="watched-badge">&#10003; Watched</span>{/if}
                  </h2>
                  {#if !playing}
                    <div class="title-actions">
                      <button
                        class="heart"
                        class:on={detail.favorite}
                        title={detail.favorite ? 'Remove from favorites' : 'Add to favorites'}
                        onclick={toggleFavorite}>{detail.favorite ? '♥' : '♡'}</button>
                      <button class="play" onclick={() => playFile(currentFile)}>&#9654; {hasResume ? 'Continue' : 'Play'}</button>
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
                  <table><tbody>
                    {#each detail.metadata as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
                  </tbody></table>
                {/if}

                {#if detail.ratings.length}
                  <h3>Ratings</h3>
                  <table><tbody>
                    {#each detail.ratings as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
                  </tbody></table>
                {/if}

                {#if detail.technical.length}
                  <h3>Technical</h3>
                  <table><tbody>
                    {#each detail.technical as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
                  </tbody></table>
                {/if}

                {#if detail.actors.length}
                  <h3>Cast</h3>
                  <ul>{#each detail.actors as a}<li>{a}</li>{/each}</ul>
                {/if}

                {#if detail.plot}<h3>Plot</h3><p>{detail.plot}</p>{/if}
              </div>

              {#if detail.hasPoster}
                <aside class="detail-poster">
                  <img src={'/api/media/' + detail.id + '/poster?size=detail'} alt={detail.title} />
                </aside>
              {/if}
            </div>
          {:else}
            <p class="center">Loading...</p>
          {/if}
        {:else if libMode === 'category'}
          <div class="cat-head">
            <h1>{homeCategoryAlias(homeCategory)}</h1>
            {#if categoryMedia.length}
              <button class="tok-launch" onclick={startTokTok}>&#9654; TokTok Mode</button>
            {/if}
          </div>
          {#if categoryMedia.length}
            <div class="grid">
              {#each categoryMedia as m}{@render tile(m, null, true)}{/each}
            </div>
          {:else}
            <p class="center">No media in this category yet.</p>
          {/if}
        {:else}
          <h2 class="row-title">Continue watching</h2>
          {#if homeData.continue.length}
            <div class="grid">
              {#each homeData.continue as m}{@render tile(m, removeFromContinue, false)}{/each}
            </div>
          {:else}
            <p class="center">Nothing in progress - pick a category to start watching.</p>
          {/if}
          {#if homeData.favorites.length}
            <h2 class="row-title">Favorites</h2>
            <div class="grid">
              {#each homeData.favorites as m}{@render tile(m, removeFromFavorites, false)}{/each}
            </div>
          {/if}
          {#if homeData.completed.length}
            <h2 class="row-title">Completed</h2>
            <div class="grid">
              {#each homeData.completed as m}{@render tile(m, removeFromCompleted, false)}{/each}
            </div>
          {/if}
        {/if}
      {:else if adminView === 'library' && importPage === ''}
        <h1>Library</h1>
        {#if catError}<p class="error">{catError}</p>{/if}
        <table class="cat-table">
          <thead>
            <tr>
              <th>Folder</th>
              <th>Alias</th>
              <th title="Other media (home videos / recordings): skips OMDb lookups and derives posters from a video frame instead.">Other media</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each categoryTree as c}
              <tr>
                <td><span class="cat-tree">{treeMarker(c._depth)}</span>{c.leaf ?? c.name}</td>
                <td>
                  {#if editName === c.name}
                    <input class="alias-input" bind:value={editAlias} onkeydown={(e) => e.key === 'Enter' && saveAlias()} />
                  {:else}
                    {c.alias}
                  {/if}
                </td>
                <td class="cat-other">
                  {#if c._depth === 0}
                    <input
                      type="checkbox"
                      checked={c.otherMedia}
                      onchange={(e) => toggleOtherMedia(c, e.currentTarget.checked)} />
                  {:else}
                    <span class="cat-inherited" title="Inherited from the top-level category">inherited</span>
                  {/if}
                </td>
                <td class="cat-action">
                  {#if editName === c.name}
                    <button onclick={saveAlias}>Save</button>
                    <button class="link" onclick={() => (editName = '')}>Cancel</button>
                  {:else}
                    <button onclick={() => startEditAlias(c)}>Edit</button>
                    <button
                      class="danger"
                      disabled={!c.empty}
                      title={c.empty ? 'Delete this empty category' : 'Folder is not empty'}
                      onclick={() => deleteCategory(c.name)}>Delete</button>
                  {/if}
                </td>
              </tr>
            {/each}
            {#if categories.length === 0}
              <tr><td colspan="4" class="cat-empty">No categories yet.</td></tr>
            {/if}
          </tbody>
        </table>
        <div class="cat-add">
          <input placeholder="Folder name" bind:value={catName} />
          <input placeholder="Alias (defaults to folder name)" bind:value={catAlias} />
          <select bind:value={catParentId}>
            <option value={0}>(top level)</option>
            {#each categoryTree as c}
              <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
            {/each}
          </select>
          <button disabled={!catName.trim()} onclick={addCategory}>Add</button>
        </div>

        <h1 class="import-head">Import</h1>
        {#if mediaFormat === ''}
          <p class="import-gate">
            To import media, please select a media format first.
            <button class="link" onclick={openSettings}>Go to Settings</button>
          </p>
        {:else if pendingImports.length > 0}
          <p class="import-gate">
            An import was started.
            <button class="link" onclick={() => go('/admin/library/import')}>Continue import</button>
          </p>
        {:else}
          <div class="import-row">
            <select bind:value={importSource}>
              <option value="folder">import folder</option>
              <option value="upload">upload files</option>
              <option value="plex">Plex library</option>
              <option value="jellyfin">Jellyfin library</option>
            </select>
            {#if importSource !== 'plex' && importSource !== 'jellyfin'}
              <select bind:value={importCategory}>
                {#each categoryTree as c}
                  <option value={c.name}>{treeMarker(c._depth)}{c.alias}</option>
                {/each}
              </select>
            {/if}
            <button
              disabled={importSource !== 'plex' && importSource !== 'jellyfin' && !importCategory}
              onclick={startImport}>Import</button>
          </div>
        {/if}
      {:else if adminView === 'library'}
        <button class="link" onclick={() => go('/admin/library')}>&larr; Back</button>
        {#if importOrigin === 'plex'}
          <h1>Import from Plex</h1>
        {:else if importOrigin === 'jellyfin'}
          <h1>Import from Jellyfin</h1>
        {:else}
          <h1>Import into {categoryAlias(importCategory)}</h1>
        {/if}

        {#if importPage === 'plex'}
          <p class="field-label">Plex database</p>
          <div class="import-row">
            <input class="plex-db-input" bind:value={plexDB} placeholder="path to com.plexapp.plugins.library.db" />
            <button type="button" onclick={openPlexBrowse}>Browse</button>
            <button disabled={!plexDB.trim() || plexChecking} onclick={plexCheck}>
              {plexChecking ? 'Checking...' : 'Check'}
            </button>
          </div>
          <details class="plex-advanced">
            <summary>Advanced</summary>
            <p class="field-label">Metadata directory (override; defaults to the Plex Metadata folder)</p>
            <input class="plex-db-input" bind:value={plexMetaDir} placeholder="(auto)" />
          </details>
          {#if plexError}<p class="error">{plexError}</p>{/if}

          {#if plexBrowseOpen}
            <div class="browser">
              <div class="browser-path">{plexBrowse.path}</div>
              {#if plexBrowseError}<p class="error">{plexBrowseError}</p>{/if}
              <ul class="browser-list">
                {#if plexBrowse.parent}
                  <li><button type="button" onclick={() => plexBrowseNavigate(plexBrowse.parent)}>.. (up)</button></li>
                {/if}
                {#each plexBrowse.entries as e}
                  <li><button type="button" onclick={() => pickPlexDB(e)}>{e.isDir ? e.name + '/' : e.name}</button></li>
                {/each}
              </ul>
            </div>
          {/if}

          {#if plexChecked}
            {#if plexStaging || plexProgress}
              <p class="field-label">Loading media files</p>
              {@const total = plexProgress?.total || 0}
              {@const done = plexProgress?.done || 0}
              <div class="prog-bar"><div class="prog-fill" style="width: {total ? Math.round((done / total) * 100) : 0}%"></div></div>
              <p class="center">{done} / {total} files - {plexProgress?.staged || 0} staged, {plexProgress?.missing || 0} missing</p>
            {:else}
              <table class="cat-table">
                <thead>
                  <tr><th></th><th>Library</th><th>Items</th><th>Category</th><th>Path status</th></tr>
                </thead>
                <tbody>
                  {#each plexRows as row}
                    <tr>
                      <td><input type="checkbox" checked={row.selected} onchange={() => togglePlexRow(row)} /></td>
                      <td>{row.section}</td>
                      <td>{row.count}</td>
                      <td>
                        <select bind:value={row.categoryId}>
                          <option value={0}>Create category from Plex</option>
                          {#each categoryTree as c}
                            <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
                          {/each}
                        </select>
                      </td>
                      <td>
                        {#if !row.selected}
                          <span class="plex-status muted">-</span>
                        {:else if row.resolving}
                          <span class="plex-status">checking...</span>
                        {:else if row.status === 'green'}
                          <span class="plex-status ok" title={row.to ? 'resolved -> ' + row.to : ''}>
                            paths OK ({row.found}/{row.total}){#if row.to} &rarr; <code>{row.to}</code>{/if}
                          </span>
                        {:else if row.status === 'needsInput' || row.status === 'unresolved'}
                          <div class="plex-fix">
                            <span class="plex-status {row.status === 'unresolved' ? 'bad' : 'warn'}">
                              {row.status === 'unresolved' ? 'paths not found' : 'enter media location'}
                              {#if row.total}({row.found}/{row.total}){/if}
                            </span>
                            <input
                              class="plex-base-input"
                              bind:value={row.searchBase}
                              placeholder="media location (folder the files live under)"
                              onkeydown={(e) => e.key === 'Enter' && plexResolveRow(row)} />
                            <button type="button" disabled={!row.searchBase.trim() || row.resolving} onclick={() => plexResolveRow(row)}>Recheck</button>
                          </div>
                        {/if}
                      </td>
                    </tr>
                  {/each}
                  {#if plexRows.length === 0}
                    <tr><td colspan="5" class="cat-empty">No movie or show libraries found.</td></tr>
                  {/if}
                </tbody>
              </table>
              <button class="import-go" disabled={!plexReady} onclick={startPlexStaging}>Load media files</button>
            {/if}
          {/if}
        {:else if importPage === 'jellyfin'}
          <p class="field-label">Jellyfin library folder (on the server)</p>
          <div class="import-row">
            <input class="plex-db-input" bind:value={jellyfinDir} placeholder="path to the NFO library directory" />
            <button type="button" onclick={openJellyfinBrowse}>Browse</button>
          </div>
          {#if jellyfinError}<p class="error">{jellyfinError}</p>{/if}

          {#if jellyfinBrowseOpen}
            <div class="browser">
              <div class="browser-path">{jellyfinBrowse.path}</div>
              {#if jellyfinBrowseError}<p class="error">{jellyfinBrowseError}</p>{/if}
              <ul class="browser-list">
                {#if jellyfinBrowse.parent}
                  <li><button type="button" onclick={() => jellyfinBrowseNavigate(jellyfinBrowse.parent)}>.. (up)</button></li>
                {/if}
                {#each jellyfinBrowse.entries as e}
                  <li><button type="button" onclick={() => jellyfinBrowseNavigate(e.path)}>{e.name}/</button></li>
                {/each}
              </ul>
              <button type="button" class="browser-select" onclick={selectJellyfinDir}>Select this folder</button>
            </div>
          {/if}

          {#if jellyfinStaging || jellyfinProgress}
            <p class="field-label">Loading media files</p>
            {@const total = jellyfinProgress?.total || 0}
            {@const done = jellyfinProgress?.done || 0}
            <div class="prog-bar"><div class="prog-fill" style="width: {total ? Math.round((done / total) * 100) : 0}%"></div></div>
            <p class="center">{done} / {total} files - {jellyfinProgress?.staged || 0} staged, {jellyfinProgress?.missing || 0} missing</p>
          {:else}
            <p class="field-label">Target category</p>
            <div class="import-row">
              <select bind:value={jellyfinCategoryId}>
                <option value={0}>Create a new category</option>
                {#each categoryTree as c}
                  <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
                {/each}
              </select>
              {#if jellyfinCategoryId === 0}
                <input placeholder="New category folder name" bind:value={jellyfinNewName} />
              {/if}
            </div>
            <button class="import-go" disabled={!jellyfinReady} onclick={startJellyfinStaging}>Load media files</button>
          {/if}
        {:else if importPage === 'upload'}
          <p class="field-label">Select one or more files from your computer.</p>
          <input type="file" multiple onchange={onUploadPick} disabled={uploading} />
          {#if uploadError}<p class="error">{uploadError}</p>{/if}
          {#if uploadProgress.length}
            <ul class="upload-list">
              {#each uploadProgress as p}
                <li class="upload-item">
                  <span class="upload-name">{p.name}</span>
                  <progress class="upload-bar" max="100" value={p.pct}></progress>
                  <span class="upload-pct">{p.status === 'error' ? 'failed' : p.pct + '%'}</span>
                </li>
              {/each}
            </ul>
          {/if}
          <button class="import-go" disabled={!uploadFiles.length || uploading} onclick={startUpload}>
            {uploading ? 'Uploading...' : `Upload ${uploadFiles.length} file${uploadFiles.length === 1 ? '' : 's'}`}
          </button>
        {:else}
          <!-- assessment of the staged files (configured folder or uploaded set) -->
          {#if importOrigin === 'plex'}
            <p class="field-label">Assessment of the Plex import</p>
          {:else if importOrigin === 'jellyfin'}
            <p class="field-label">Assessment of the Jellyfin import</p>
          {:else if importOrigin === 'upload'}
            <p class="field-label">Assessment of the uploaded files</p>
          {:else}
            <p class="field-label">Assessment of <code>{importFolder}</code></p>
          {/if}
          {#if assessError}<p class="error">{assessError}</p>{/if}
          {#if assessLoading}
            <p class="center">Scanning and looking up metadata...</p>
          {:else}
            <table class="cat-table">
              <thead>
                <tr><th>Category</th><th>Media files</th><th>Title</th><th>Year</th><th>Poster</th><th>Subs</th><th></th></tr>
              </thead>
              <tbody>
                {#each assessGroups as group}
                  <tr>
                    <td>
                      {#if editKey === group.key}
                        <select bind:value={editCategory}>
                          {#each categoryTree as c}
                            <option value={c.name}>{treeMarker(c._depth)}{c.alias}</option>
                          {/each}
                        </select>
                      {:else}
                        {categoryAlias(group.category)}
                      {/if}
                    </td>
                    <td>{group.count}</td>
                    <td>
                      {#if editKey === group.key}
                        <input class="alias-input" bind:value={editTitle} onkeydown={(e) => e.key === 'Enter' && saveImportEdit()} />
                      {:else}
                        {group.title || '(unknown)'}
                      {/if}
                    </td>
                    <td>
                      {#if editKey === group.key}
                        <input class="year-input" type="text" bind:value={editYear} onkeydown={(e) => e.key === 'Enter' && saveImportEdit()} />
                      {:else}
                        {group.year || ''}
                      {/if}
                    </td>
                    <td>{#if group.hasPoster}<span class="poster-ok" title="Poster found">&#10003;</span>{/if}</td>
                    <td>{#if group.subCount > 0}<span class="poster-ok" title="Subtitle files found">{group.subCount}</span>{/if}</td>
                    <td class="cat-action">
                      {#if editKey === group.key}
                        <button onclick={saveImportEdit}>Save</button>
                        <button class="link" onclick={() => (editKey = '')}>Cancel</button>
                      {:else}
                        <button onclick={() => startEditImport(group)}>Edit</button>
                        <button class="danger" title="Remove this media from the import" onclick={() => deleteImportRow(group)}>X</button>
                      {/if}
                    </td>
                  </tr>
                {/each}
                {#if assessGroups.length === 0}
                  <tr><td colspan="7" class="cat-empty">No media files found.</td></tr>
                {/if}
              </tbody>
            </table>
            <label class="delete-after">
              <input
                type="checkbox"
                bind:checked={deleteAfter}
                disabled={importOrigin === 'upload' || importOrigin === 'plex'} />
              {#if importOrigin === 'plex'}
                Plex originals are never touched
              {:else if importOrigin === 'upload'}
                Uploaded files are always removed after a successful import
              {:else}
                Delete originals from the import folder after a successful import
              {/if}
            </label>
            <button class="import-go" disabled={assessRows.length === 0} onclick={startImportBatch}>Start import</button>
          {/if}
        {/if}
      {:else if adminView === 'settings' && mediaFormat === ''}
        <h1>Choose a media format</h1>
        <p class="settings-intro">
          This choice is <strong>permanent</strong>. It dictates how and in what format media files are
          organized in the app after import. The on-disk layout is always
          <code>{'{dataDir}/{category}/{mediafolder}/'}</code>; the format sets the naming style of the
          media folder and its files.
        </p>
        {#if settingsError}<p class="error">{settingsError}</p>{/if}
        <div class="format-boxes">
          {#each formatBoxes as b}
            <button
              type="button"
              class="format-box"
              class:selected={formatChoice === b.id}
              onclick={() => (formatChoice = b.id)}>
              <span class="format-title">{b.title}</span>
              <span class="format-desc">{b.desc}</span>
              <code>{b.movie}</code>
              <code>{b.episode}</code>
            </button>
          {/each}
        </div>
        <button class="import-go" disabled={!formatChoice} onclick={selectFormat}>Permanently select</button>
      {:else if adminView === 'settings'}
        <div class="page-head">
          <h1>Settings</h1>
          <button class="rebuild-btn" disabled={enrichScanning} onclick={enrichScan}>
            {enrichScanning ? 'Scanning...' : 'OMDB enrichment'}
          </button>
          <button class="rebuild-btn" disabled={optimizeScanning} onclick={optimizeScan}>
            {optimizeScanning ? 'Scanning...' : 'Optimizer scan'}
          </button>
          <button class="rebuild-btn" disabled={thumbnailScanning} onclick={thumbnailScan}>
            {thumbnailScanning ? 'Scanning...' : 'Thumbnail scan'}
          </button>
          <button class="rebuild-btn" disabled={rebuilding} onclick={rebuildDb}>
            {rebuilding ? 'Rebuilding...' : 'Rebuild database'}
          </button>
        </div>
        {#if settingsError}<p class="error">{settingsError}</p>{/if}
        {#if rebuildMsg}<p class="rebuild-msg">{rebuildMsg}</p>{/if}
        {#if optimizeScanMsg}<p class="rebuild-msg">{optimizeScanMsg}</p>{/if}
        {#if enrichScanMsg}<p class="rebuild-msg">{enrichScanMsg}</p>{/if}
        {#if thumbnailScanMsg}<p class="rebuild-msg">{thumbnailScanMsg}</p>{/if}
        <table class="cat-table">
          <tbody>
            {#each settings as row}
              <tr>
                <td class="settings-name">{row.name}</td>
                <td>
                  {#if row.name === 'OMDb API key' && editOmdb}
                    <input class="alias-input" bind:value={omdbInput} onkeydown={(e) => e.key === 'Enter' && saveOmdbKey()} />
                    <button onclick={saveOmdbKey}>Save</button>
                    <button class="link" onclick={() => (editOmdb = false)}>Cancel</button>
                  {:else if row.name === 'Log level' && editLogging}
                    <select class="log-select" bind:value={logLevelInput}>
                      <option value="error">error</option>
                      <option value="info">info</option>
                      <option value="debug">debug</option>
                    </select>
                  {:else if row.name === 'Log output' && editLogging}
                    <input class="alias-input" bind:value={logOutputInput} onkeydown={(e) => e.key === 'Enter' && saveLogging()} />
                    <button onclick={saveLogging}>Save</button>
                    <button class="link" onclick={() => (editLogging = false)}>Cancel</button>
                  {:else if row.name === 'Transcoding' && editTranscoding}
                    <label class="delete-after" style="margin:0">
                      <input type="checkbox" bind:checked={transcodeEnabledInput} /> enabled
                    </label>
                    <button onclick={saveTranscoding}>Save</button>
                    <button class="link" onclick={() => (editTranscoding = false)}>Cancel</button>
                  {:else if row.name === 'ffmpeg path' && editTranscoding}
                    <input class="alias-input" bind:value={ffmpegPathInput} onkeydown={(e) => e.key === 'Enter' && saveTranscoding()} />
                  {:else if row.name === 'ffprobe path' && editTranscoding}
                    <input class="alias-input" bind:value={ffprobePathInput} onkeydown={(e) => e.key === 'Enter' && saveTranscoding()} />
                  {:else if row.name === 'Subtitle language' && editSubtitle}
                    <input class="alias-input" bind:value={subtitleInput} onkeydown={(e) => e.key === 'Enter' && saveSubtitle()} />
                    <button onclick={saveSubtitle}>Save</button>
                    <button class="link" onclick={() => (editSubtitle = false)}>Cancel</button>
                  {:else if row.name === 'Optimizer' && editOptimizer}
                    <select class="log-select" bind:value={optimizeModeInput}>
                      {#each optimizeModes as m}
                        <option value={m.value}>{m.label}</option>
                      {/each}
                    </select>
                    <button onclick={saveOptimizer}>Save</button>
                    <button class="link" onclick={() => (editOptimizer = false)}>Cancel</button>
                  {:else}
                    {row.value}
                    {#if row.name === 'Import folder'}
                      <button class="settings-edit" onclick={openImportFolderBrowser}>Edit</button>
                    {:else if row.name === 'OMDb API key'}
                      <button class="settings-edit" onclick={startEditOmdb}>Edit</button>
                    {:else if row.name === 'Log level'}
                      <button class="settings-edit" onclick={startEditLogging}>Edit</button>
                    {:else if row.name === 'Transcoding'}
                      <button class="settings-edit" onclick={startEditTranscoding}>Edit</button>
                    {:else if row.name === 'Subtitle language'}
                      <button class="settings-edit" onclick={startEditSubtitle}>Edit</button>
                    {:else if row.name === 'Optimizer'}
                      <button class="settings-edit" onclick={startEditOptimizer}>Edit</button>
                    {/if}
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if ifBrowseOpen}
          <div class="browser">
            <div class="browser-path">{ifPath}</div>
            {#if ifError}<p class="error">{ifError}</p>{/if}
            <ul class="browser-list">
              {#if ifParent}
                <li><button type="button" onclick={() => importFolderNavigate(ifParent)}>.. (up)</button></li>
              {/if}
              {#each ifEntries as e}
                <li><button type="button" onclick={() => importFolderNavigate(e.path)}>{e.name}</button></li>
              {/each}
            </ul>
            <button type="button" class="browser-select" onclick={selectImportFolder}>Select this folder</button>
          </div>
        {/if}
      {:else if adminView === 'users'}
        <h1>Users</h1>
        {#if usersError}<p class="error">{usersError}</p>{/if}
        <table class="cat-table">
          <thead>
            <tr><th>ID</th><th>Email</th><th>Alias</th><th>Role</th><th>Status</th><th>Last login</th><th></th></tr>
          </thead>
          <tbody>
            {#each users as u}
              <tr>
                <td>{u.id}</td>
                <td>{u.username}</td>
                <td>
                  {#if editUserId === u.id}
                    <input class="alias-input" bind:value={editUserAlias} onkeydown={(e) => e.key === 'Enter' && saveUserAlias(u)} />
                  {:else}
                    {u.alias || '-'}
                  {/if}
                </td>
                <td>{u.admin ? 'admin' : 'user'}</td>
                <td>{u.blocked ? 'blocked' : 'active'}</td>
                <td>{fmtTime(u.lastLoginAt)}</td>
                <td class="cat-action">
                  {#if editUserId === u.id}
                    <button onclick={() => saveUserAlias(u)}>Save</button>
                    <button class="link" onclick={() => (editUserId = 0)}>Cancel</button>
                  {:else}
                    <button onclick={() => startEditUser(u)}>Edit</button>
                    {#if u.username !== me.user}
                      <button onclick={() => patchUser(u, { admin: !u.admin })}>{u.admin ? 'Revoke admin' : 'Make admin'}</button>
                      <button class={u.blocked ? '' : 'danger'} onclick={() => patchUser(u, { blocked: !u.blocked })}>
                        {u.blocked ? 'Unblock' : 'Block'}
                      </button>
                    {/if}
                  {/if}
                </td>
              </tr>
            {/each}
            {#if users.length === 0}
              <tr><td colspan="7" class="cat-empty">No users.</td></tr>
            {/if}
          </tbody>
        </table>
        <div class="cat-add">
          <input placeholder="Email" bind:value={newUserEmail} autocomplete="off" />
          <input placeholder="Alias" bind:value={newUserAlias} />
          <input type="password" placeholder="Password" bind:value={newUserPassword} autocomplete="new-password" />
          <label class="user-admin"><input type="checkbox" bind:checked={newUserAdmin} /> admin</label>
          <button disabled={!newUserReady} onclick={addUser}>Add user</button>
        </div>
      {:else if adminView === 'progress'}
        <h1>Progress</h1>
        <h2 class="prog-section">Imports</h2>
        {#if importActive.length === 0}
          <p class="import-gate">No imports running.</p>
        {:else}
          <table class="cat-table">
            <thead>
              <tr><th>Category</th><th>Title</th><th>Progress</th></tr>
            </thead>
            <tbody>
              {#each importActive as row}
                <tr>
                  <td>{row.category}</td>
                  <td>{row.title || row.filename}</td>
                  <td>
                    <div class="prog-bar"><div class="prog-fill" style="width: {pct(row)}%"></div></div>
                    <span class="prog-pct">{pct(row)}%</span>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
        {#if importPending > 0}
          <p class="prog-waiting">{importPending} more waiting in line</p>
        {/if}
        <h2 class="prog-section">Optimizing</h2>
        {#if optimizeRows.length === 0}
          <p class="import-gate">No encodes running.</p>
        {:else}
          <table class="cat-table">
            <thead>
              <tr><th>Title</th><th>File</th><th>Agent</th><th>Progress</th></tr>
            </thead>
            <tbody>
              {#each optimizeRows as row}
                <tr>
                  <td>{row.title}</td>
                  <td>{row.file}</td>
                  <td>{row.agent}</td>
                  <td>
                    <div class="prog-bar"><div class="prog-fill" style="width: {row.percent}%"></div></div>
                    <span class="prog-pct">{row.percent}%</span>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
        {#if optimizePending > 0}
          <p class="prog-waiting">{optimizePending} more waiting in line</p>
        {/if}
        <h2 class="prog-section">Enriching</h2>
        {#if enrichRows.length === 0}
          <p class="import-gate">No enrichment running.</p>
        {:else}
          <table class="cat-table">
            <thead>
              <tr><th>Title</th><th>Agent</th><th>Status</th></tr>
            </thead>
            <tbody>
              {#each enrichRows as row}
                <tr>
                  <td>{row.title}</td>
                  <td>{row.agent}</td>
                  <td>looking up...</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
        {#if enrichPending > 0}
          <p class="prog-waiting">{enrichPending} more waiting in line</p>
        {/if}
        <h2 class="prog-section">Thumbnails</h2>
        {#if thumbnailRows.length === 0}
          <p class="import-gate">No thumbnails running.</p>
        {:else}
          <table class="cat-table">
            <thead>
              <tr><th>Title</th><th>Agent</th><th>Status</th></tr>
            </thead>
            <tbody>
              {#each thumbnailRows as row}
                <tr>
                  <td>{row.title}</td>
                  <td>{row.agent}</td>
                  <td>generating...</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
        {#if thumbnailPending > 0}
          <p class="prog-waiting">{thumbnailPending} more waiting in line</p>
        {/if}
      {:else}
        <h1>Dashboard</h1>
        {#if summary}
          <div class="dash">
            <div class="dash-card">
              <span class="dash-num">{summary.library.media}</span>
              <span class="dash-label">Media in {summary.library.categories} categories</span>
            </div>
            <div class="dash-card">
              <span class="dash-num">{summary.library.files}</span>
              <span class="dash-label">Media files</span>
            </div>
            <div class="dash-card">
              <span class="dash-num">{summary.users.total}</span>
              <span class="dash-label">Users ({summary.users.admins} admin)</span>
            </div>
            <div class="dash-card">
              <span class="dash-num">{summary.optimizer.active}</span>
              <span class="dash-label">Optimizing - {summary.optimizer.pending} queued ({summary.optimizer.mode})</span>
            </div>
            <div class="dash-card">
              <span class="dash-num">{summary.enrich.pending}</span>
              <span class="dash-label">Enrich queued</span>
            </div>
            <div class="dash-card">
              <span class="dash-num">{summary.imports.active}</span>
              <span class="dash-label">Imports running</span>
            </div>
          </div>
        {:else}
          <p class="center">Loading...</p>
        {/if}
      {/if}
    </main>
  </div>
{/if}

<svelte:window onkeydown={tokKeydown} />

{#if tokOn}
  <div class="tok">
    <div class="tok-bar">
      <span class="tok-title">{tokTitle}</span>
      <div class="tok-actions">
        <button class="tok-btn" title="Next video" onclick={advanceTok}>&#9197;</button>
        <button class="tok-btn" title="Close (Esc)" onclick={stopTokTok}>&#10005;</button>
      </div>
    </div>
    <video class="tok-video" controls autoplay playsinline bind:this={tokVideoEl}></video>
  </div>
{/if}

{#snippet tile(m, onRemove, showWatched)}
  <div class="tile">
    <button class="card" onclick={() => openMedia(m)}>
      <div class="poster">
        {#if m.hasPoster}
          <img src={'/api/media/' + m.id + '/poster?size=tile'} alt={m.title} />
        {:else}
          <div class="noposter">{m.title}</div>
        {/if}
        {#if showWatched && m.watched}
          <span class="card-watched" title="Watched">&#10003;</span>
        {/if}
      </div>
      <span>{m.title}</span>
      <span class="year">{m.year}</span>
    </button>
    {#if onRemove}
      <button class="tile-remove" title="Remove" onclick={(e) => { e.stopPropagation(); onRemove(m) }}>&#10005;</button>
    {/if}
  </div>
{/snippet}
