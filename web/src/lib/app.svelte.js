import { api, errText } from './api.js'

// --- pure helpers (no app state) ---

// groupSeasons buckets a media item's files by season, sorted, each flagged watched when all
// its episodes are watched.
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
export function episodeLabel(f) {
  return f.episode ? 'E' + f.episode : f.name
}

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
  for (const list of byParent.values())
    list.sort((a, b) => (a.position ?? 0) - (b.position ?? 0) || (a.leaf ?? a.name).localeCompare(b.leaf ?? b.name))
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
export function treeMarker(depth) {
  if (!depth || depth <= 0) return ''
  return '  '.repeat(depth - 1) + '└─ '
}

export function fmtTime(ts) {
  if (!ts) return 'never'
  const d = new Date(ts * 1000)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export function pct(row) {
  if (!row.total) return 0
  return Math.min(100, Math.round((row.copied / row.total) * 100))
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

// AppState owns every piece of frontend state and the logic that mutates it. A single
// instance is created in App.svelte and shared with every view through context, so the
// state graph stays exactly as it was when this all lived in one component.
export class AppState {
  booting = $state(true)
  needsSetup = $state(false)
  me = $state(null) // { user, admin }
  view = $state('library') // 'library' | 'admin'
  adminView = $state('dashboard') // 'dashboard' | 'library' | 'users' | 'settings' | 'progress'

  // install form
  iuser = $state('')
  ipass = $state('')
  iport = $state(8080)
  installError = $state('')

  // login form
  luser = $state('')
  lpass = $state('')
  loginError = $state('')

  // data folder picker (install form)
  dataDir = $state('')
  browseOpen = $state(false)
  browsePath = $state('')
  browseParent = $state('')
  browseEntries = $state([])
  browseError = $state('')

  // end-user home: category aliases shown as nav links under Home
  homeCategories = $state([])
  homeCategory = $state('') // selected category name, '' = Home

  // library views: home grids, category grid, media detail + player
  libMode = $state('home') // 'home' | 'category' | 'detail'
  homeData = $state({ continue: [], favorites: [], completed: [] })
  categoryMedia = $state([])
  detail = $state(null)
  currentFile = $state(0)
  currentSeason = $state(null)
  playing = $state(false)
  videoEl = $state(null)
  pendingSeek = 0

  // TokTok mode
  tokOn = $state(false)
  tokVideoEl = $state(null)
  tokMediaIdx = $state(0)
  tokFiles = $state([])
  tokFileIdx = $state(0)
  tokMediaId = $state('')
  tokTitle = $state('')
  tokCurrent = $state(null) // { mediaId, file, transcode, subtitles }
  tokHls = null

  // admin library (category management)
  categories = $state([])
  catName = $state('')
  catAlias = $state('')
  catParentId = $state(0) // 0 = create a top-level category
  catError = $state('')
  dragName = $state('') // category being dragged to reorder; '' when not dragging

  // admin settings (media-format gate + read-only config list)
  mediaFormat = $state('')
  importFolder = $state('')
  omdbKey = $state('')
  logLevel = $state('info')
  logOutput = $state('STDOUT')
  transcodeEnabled = $state(true)
  ffmpegPath = $state('ffmpeg')
  ffprobePath = $state('ffprobe')
  subtitleLanguage = $state('en')
  optimizeMode = $state('none')
  settings = $state([]) // [{name, value}]
  formatChoice = $state('')
  settingsError = $state('')

  editOmdb = $state(false)
  omdbInput = $state('')

  editLogging = $state(false)
  logLevelInput = $state('info')
  logOutputInput = $state('STDOUT')

  editTranscoding = $state(false)
  transcodeEnabledInput = $state(true)
  ffmpegPathInput = $state('ffmpeg')
  ffprobePathInput = $state('ffprobe')

  editSubtitle = $state(false)
  subtitleInput = $state('en')

  optimizeModes = [
    { value: 'none', label: 'NONE' },
    { value: 'cpu', label: 'CPU only' },
    { value: 'gpu', label: 'GPU only' },
    { value: 'all', label: 'ALL' },
  ]
  editOptimizer = $state(false)
  optimizeModeInput = $state('none')

  discoveryIntervals = [
    { value: 0, label: 'Off' },
    { value: 3600, label: 'Every 1 hour' },
    { value: 10800, label: 'Every 3 hours' },
    { value: 43200, label: 'Every 12 hours' },
    { value: 86400, label: 'Every 24 hours' },
  ]
  discoveryInterval = $state(0)
  editDiscovery = $state(false)
  discoveryIntervalInput = $state(0)
  discoveryRunning = $state(false)
  discoveryMsg = $state('')
  health = $state(null) // { items: [{id, title, issues:[{code,detail}], lastChecked}] }

  // import-folder picker (settings)
  ifBrowseOpen = $state(false)
  ifPath = $state('')
  ifParent = $state('')
  ifEntries = $state([])
  ifError = $state('')

  formatBoxes = [
    {
      id: 'filefin',
      title: 'FileFin (chronological)',
      desc: 'Year first, so titles sort by date.',
      movie: '(1999) The Matrix/(1999) The Matrix.mkv',
      episode: '(2002) Firefly/(2002) Firefly - 1x1.mkv',
    },
    {
      id: 'jellyfin',
      title: 'Plex / Jellyfin',
      desc: 'Year last; episodes as SxxEyy.',
      movie: 'The Matrix (1999)/The Matrix (1999).mkv',
      episode: 'Firefly (2002)/Firefly (2002) S01E01.mkv',
    },
  ]

  // rebuild + background scans
  rebuilding = $state(false)
  rebuildMsg = $state('')
  optimizeScanning = $state(false)
  optimizeScanMsg = $state('')
  enrichScanning = $state(false)
  enrichScanMsg = $state('')
  thumbnailScanning = $state(false)
  thumbnailScanMsg = $state('')
  probeScanning = $state(false)
  probeScanMsg = $state('')

  // inline alias editing in the admin table
  editName = $state('') // category being edited, '' = none
  editAlias = $state('')

  // admin import
  importCategory = $state('')
  importSource = $state('folder') // 'folder' | 'upload' | 'plex' | 'jellyfin'
  importPage = $state('') // '' = config, else 'assess' | 'upload' | 'plex' | 'jellyfin'

  uploadFiles = $state([])
  uploadProgress = $state([]) // { name, pct, status: 'pending'|'up'|'done'|'error' }
  uploading = $state(false)
  uploadError = $state('')
  importOrigin = $state('folder') // 'folder' | 'upload' | 'plex' | 'jellyfin'

  assessRows = $state([])
  assessLoading = $state(false)
  assessError = $state('')
  pendingImports = $state([])
  editKey = $state('') // media group being edited, '' = none
  editTitle = $state('')
  editYear = $state('')
  editCategory = $state('')
  deleteAfter = $state(true)

  // admin import: Plex source
  plexDB = $state('')
  plexMetaDir = $state('')
  plexChecking = $state(false)
  plexError = $state('')
  plexChecked = $state(false)
  plexRows = $state([])
  plexStaging = $state(false)
  plexProgress = $state(null)
  plexTimer = 0
  plexBrowseOpen = $state(false)
  plexBrowse = $state({ path: '', parent: '', entries: [] })
  plexBrowseError = $state('')

  // admin import: Jellyfin (NFO) source
  jellyfinDir = $state('')
  jellyfinError = $state('')
  jellyfinCategoryId = $state(0)
  jellyfinNewName = $state('')
  jellyfinStaging = $state(false)
  jellyfinProgress = $state(null)
  jellyfinTimer = 0
  jellyfinBrowseOpen = $state(false)
  jellyfinBrowse = $state({ path: '', parent: '', entries: [] })
  jellyfinBrowseError = $state('')

  // admin progress
  progressRows = $state([])
  optimizeRows = $state([])
  optimizePending = $state(0)
  enrichRows = $state([])
  enrichPending = $state(0)
  thumbnailRows = $state([])
  thumbnailPending = $state(0)
  probeRows = $state([])
  probePending = $state(0)
  progressTimer = 0

  // admin dashboard
  summary = $state(null)

  // admin users
  users = $state([])
  usersError = $state('')
  newUserEmail = $state('')
  newUserAlias = $state('')
  newUserPassword = $state('')
  newUserAdmin = $state(false)
  editUserId = $state(0)
  editUserAlias = $state('')

  // --- derived ---
  seasons = $derived(groupSeasons(this.detail))
  currentEpisodes = $derived(this.seasons.find((s) => s.season === this.currentSeason)?.episodes ?? [])
  // "Continue" rather than "Play" when there is an unfinished resume point.
  hasResume = $derived(
    !!this.detail && !this.detail.watched && (this.detail.continueIndex > 0 || this.detail.continueSeconds > 0),
  )
  categoryTree = $derived(treeOrder(this.categories))
  homeTree = $derived(treeOrder(this.homeCategories))

  // The importer stages one row per file, but the assessment view is media-centric:
  // a show's episodes share one OMDb lookup, poster, and meta, so they collapse to a
  // single row keyed by (title, year) with a count of the contained media files.
  assessGroups = $derived.by(() => {
    const map = new Map()
    for (const r of this.assessRows) {
      const key = (r.title || '') + ' ' + (r.year || 0)
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

  // a library is ready to stage when it resolves green (paths located)
  plexReady = $derived(
    this.plexRows.some((r) => r.selected) && this.plexRows.every((r) => !r.selected || r.status === 'green'),
  )
  jellyfinReady = $derived(
    !!this.jellyfinDir.trim() && (this.jellyfinCategoryId !== 0 || !!this.jellyfinNewName.trim()),
  )

  // Like the optimizer/enricher, only the rows actually copying are shown; the queued
  // ones (status 'import', not yet picked up by the poller) collapse into a footnote.
  importActive = $derived(this.progressRows.filter((r) => r.status === 'importing'))
  importPending = $derived(this.progressRows.filter((r) => r.status === 'import').length)

  newUserReady = $derived(!!this.newUserEmail.trim() && !!this.newUserPassword)

  // --- install / auth ---

  async openBrowser() {
    this.browseOpen = true
    await this.navigate('') // empty path: server starts at the app's current directory
  }

  async navigate(path) {
    this.browseError = ''
    try {
      const q = path ? '?path=' + encodeURIComponent(path) : ''
      const r = await api('/api/install/browse' + q)
      this.browsePath = r.path
      this.browseParent = r.parent
      this.browseEntries = r.entries
    } catch {
      this.browseError = 'Cannot open that folder'
    }
  }

  selectFolder() {
    this.dataDir = this.browsePath
    this.browseOpen = false
  }

  async doInstall(e) {
    e.preventDefault()
    this.installError = ''
    try {
      const r = await api('/api/install', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username: this.iuser, password: this.ipass, port: Number(this.iport), dataDir: this.dataDir }),
      })
      // The server is rebinding to the chosen port; reload the page there.
      const url = window.location.protocol + '//' + window.location.hostname + ':' + r.port + '/'
      setTimeout(() => {
        window.location.href = url
      }, 800)
    } catch {
      this.installError = 'Setup failed'
    }
  }

  async doLogin(e) {
    e.preventDefault()
    this.loginError = ''
    try {
      this.me = await api('/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username: this.luser, password: this.lpass }),
      })
      if (this.me?.admin) await this.loadSettings()
      await this.route() // honor the URL the login page sat on (deep link / refresh)
    } catch {
      this.loginError = 'Invalid credentials'
    }
  }

  async signOut() {
    try {
      await api('/api/logout', { method: 'POST' })
    } catch {}
    this.me = null
    this.luser = ''
    this.lpass = ''
    this.view = 'library'
    this.adminView = 'dashboard'
    this.homeCategory = ''
    this.libMode = 'home'
    this.detail = null
    this.playing = false
    history.replaceState({}, '', '/')
  }

  async loadHomeCategories() {
    try {
      this.homeCategories = await api('/api/categories')
    } catch {
      this.homeCategories = []
    }
  }

  homeCategoryAlias(name) {
    return this.homeCategories.find((c) => c.name === name)?.alias ?? name
  }

  // --- library ---

  async loadHome() {
    try {
      this.homeData = await api('/api/home')
    } catch {
      this.homeData = { continue: [], favorites: [], completed: [] }
    }
  }

  async loadCategoryMedia(id) {
    try {
      this.categoryMedia = await api('/api/category/' + id + '/media')
    } catch {
      this.categoryMedia = []
    }
  }

  async showMedia(id) {
    this.playing = false
    this.detail = await api('/api/media/' + id)
    // Preselect the furthest reached file (Continue); a fully watched folder starts over.
    const start = this.detail.watched ? 0 : this.detail.continueIndex || 0
    this.currentFile = this.detail.files[start]?.index ?? 0
    const groups = groupSeasons(this.detail)
    this.currentSeason = seasonOfFile(groups, this.currentFile, groups.length ? groups[0].season : null)
  }

  openMedia(m) {
    this.go('/media/' + m.id)
  }

  playFile(idx) {
    // Resume to the saved second only when starting the furthest-unfinished file; an
    // explicit pick of any other file starts it from the beginning.
    const resumeIdx = this.detail && !this.detail.watched ? this.detail.continueIndex || 0 : -1
    this.pendingSeek = idx === (this.detail?.files[resumeIdx]?.index ?? -1) ? this.detail.continueSeconds || 0 : 0
    this.currentFile = idx
    this.playing = true
  }

  async toggleFavorite() {
    const next = !this.detail.favorite
    this.detail.favorite = next // optimistic
    try {
      await api('/api/media/' + this.detail.id + '/favorite', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ favorite: next }),
      })
    } catch {
      this.detail.favorite = !next // revert on failure
    }
  }

  // Home tile "x": remove from a row by clearing the matching status.
  async removeFromContinue(m) {
    this.homeData.continue = this.homeData.continue.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/progress', { method: 'DELETE' }).catch(() => {})
  }
  async removeFromFavorites(m) {
    this.homeData.favorites = this.homeData.favorites.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/favorite', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ favorite: false }),
    }).catch(() => {})
  }
  async removeFromCompleted(m) {
    this.homeData.completed = this.homeData.completed.filter((x) => x.id !== m.id)
    await api('/api/media/' + m.id + '/watched', { method: 'DELETE' }).catch(() => {})
  }

  // Best-effort progress report; failures are ignored. sendBeacon is used for page/tab
  // teardown where a normal fetch may be cancelled.
  reportProgress(mediaId, file, event, useBeacon = false, el = this.videoEl) {
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

  // --- TokTok mode ---

  async startTokTok() {
    if (!this.categoryMedia.length || this.tokOn) return
    this.tokOn = true
    await this.loadTokMedia(0)
  }

  stopTokTok() {
    this.tokOn = false
    this.tokCurrent = null
    if (this.tokHls) {
      this.tokHls.destroy()
      this.tokHls = null
    }
    if (document.fullscreenElement) document.exitFullscreen?.().catch(() => {})
  }

  // loadTokMedia fetches one category item's files and starts playing, skipping any item
  // that fails to load or has no files. Going forward it starts the first file and stops
  // past the end of the category; going back (fromEnd) it starts the last file and ignores
  // stepping before the very first video.
  async loadTokMedia(idx, fromEnd = false) {
    if (idx < 0) return // already at the first video: nothing earlier to play
    if (idx >= this.categoryMedia.length) {
      this.stopTokTok()
      return
    }
    try {
      const d = await api('/api/media/' + this.categoryMedia[idx].id)
      const files = (d.files || [])
        .slice()
        .sort((a, b) => (a.season || 0) - (b.season || 0) || (a.episode || 0) - (b.episode || 0) || a.index - b.index)
      if (!files.length) {
        await this.loadTokMedia(fromEnd ? idx - 1 : idx + 1, fromEnd)
        return
      }
      this.tokMediaIdx = idx
      this.tokMediaId = d.id
      this.tokTitle = d.title
      this.tokFiles = files
      this.playTokFile(fromEnd ? files.length - 1 : 0)
    } catch {
      await this.loadTokMedia(fromEnd ? idx - 1 : idx + 1, fromEnd)
    }
  }

  playTokFile(i) {
    const f = this.tokFiles[i]
    if (!f) {
      this.advanceTok()
      return
    }
    this.tokFileIdx = i
    this.tokCurrent = { mediaId: this.tokMediaId, file: f.index, transcode: f.transcode, subtitles: f.subtitles || [] }
  }

  // advanceTok plays the next file of the current item, else the next category item.
  advanceTok() {
    if (this.tokFileIdx + 1 < this.tokFiles.length) {
      this.playTokFile(this.tokFileIdx + 1)
    } else {
      this.loadTokMedia(this.tokMediaIdx + 1)
    }
  }

  // previousTok is the inverse: the previous file of the current item, else the last file
  // of the previous category item.
  previousTok() {
    if (this.tokFileIdx > 0) {
      this.playTokFile(this.tokFileIdx - 1)
    } else {
      this.loadTokMedia(this.tokMediaIdx - 1, true)
    }
  }

  tokKeydown(e) {
    if (!this.tokOn) return
    if (e.key === 'Escape') {
      this.stopTokTok()
    } else if (e.key === 'ArrowRight') {
      e.preventDefault() // don't let the native player seek instead
      this.advanceTok()
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      this.previousTok()
    }
  }

  // --- client routing (History API) ---
  // go() pushes a URL then applies it; route() applies the current URL without pushing.
  go(path) {
    history.pushState({}, '', path)
    this.route()
  }

  async route() {
    const segs = location.pathname.split('/').filter(Boolean)
    if (segs[0] === 'admin' && this.me?.admin) {
      this.view = 'admin'
      const page = ['dashboard', 'library', 'users', 'settings', 'progress'].includes(segs[1]) ? segs[1] : 'dashboard'
      this.applyAdmin(page, segs[2])
      return
    }
    // Library (also the fallback for non-admins hitting /admin).
    this.view = 'library'
    await this.loadHomeCategories()
    this.playing = false // a route change tears the player down (its effect cleanup reports a stop)
    if (segs[0] === 'media' && segs[1]) {
      this.libMode = 'detail'
      await this.showMedia(segs[1])
    } else if (segs[0] === 'category' && segs[1]) {
      const c = this.homeCategories.find((x) => String(x.id) === segs[1])
      this.homeCategory = c ? c.name : ''
      this.libMode = 'category'
      this.detail = null
      await this.loadCategoryMedia(segs[1])
    } else {
      this.homeCategory = ''
      this.libMode = 'home'
      this.detail = null
      await this.loadHome()
    }
  }

  // applyAdmin sets the admin sub-view and loads its data, without touching history.
  // sub is the optional third path segment ("import" resumes a prepared import).
  applyAdmin(page, sub) {
    if (page !== 'progress') this.stopProgressPoll() // leaving Progress stops its poller
    this.adminView = page
    if (page === 'library') {
      clearInterval(this.plexTimer) // stop any orphaned Plex staging poll
      this.plexTimer = 0
      clearInterval(this.jellyfinTimer) // stop any orphaned Jellyfin staging poll
      this.jellyfinTimer = 0
      this.importPage = ''
      this.editName = ''
      this.loadCategories()
      this.loadPendingImports().then(() => {
        if (sub === 'import' && this.pendingImports.length > 0) this.continueImport()
      })
    } else if (page === 'settings') {
      this.formatChoice = ''
      this.settingsError = ''
      this.rebuildMsg = ''
      this.ifBrowseOpen = false
      this.editOmdb = false
      this.editLogging = false
      this.editTranscoding = false
      this.editSubtitle = false
      this.loadSettings()
    } else if (page === 'users') {
      this.editUserId = 0
      this.loadUsers()
    } else if (page === 'dashboard') {
      this.summary = null
      this.loadSummary()
    } else if (page === 'progress') {
      this.startProgressPoll()
    }
  }

  showLibrary() {
    this.go('/')
  }

  goAdmin() {
    this.go('/admin/' + this.adminView)
  }

  async loadCategories() {
    this.catError = ''
    try {
      this.categories = await api('/api/admin/categories')
      if (!this.categories.some((c) => c.name === this.importCategory)) {
        this.importCategory = this.categories[0]?.name ?? ''
      }
    } catch {
      this.catError = 'Could not load categories'
    }
  }

  openAdminLibrary() {
    this.go('/admin/library')
  }

  // --- admin settings ---

  startEditOmdb() {
    this.omdbInput = this.omdbKey
    this.editOmdb = true
    this.settingsError = ''
  }

  async saveOmdbKey() {
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/omdb-key', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ key: this.omdbInput.trim() }),
      })
      this.omdbKey = r.omdbKey
      this.settings = r.settings
      this.editOmdb = false
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save the OMDb key'
    }
  }

  startEditLogging() {
    this.logLevelInput = this.logLevel
    this.logOutputInput = this.logOutput
    this.editLogging = true
    this.settingsError = ''
  }

  async saveLogging() {
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/logging', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ level: this.logLevelInput, output: this.logOutputInput.trim() }),
      })
      this.logLevel = r.logLevel
      this.logOutput = r.logOutput
      this.settings = r.settings
      this.editLogging = false
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save logging settings'
    }
  }

  startEditTranscoding() {
    this.transcodeEnabledInput = this.transcodeEnabled
    this.ffmpegPathInput = this.ffmpegPath
    this.ffprobePathInput = this.ffprobePath
    this.editTranscoding = true
    this.settingsError = ''
  }

  async saveTranscoding() {
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/transcoding', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          ffmpegPath: this.ffmpegPathInput.trim(),
          ffprobePath: this.ffprobePathInput.trim(),
          enabled: this.transcodeEnabledInput,
        }),
      })
      this.transcodeEnabled = r.transcodeEnabled
      this.ffmpegPath = r.ffmpegPath
      this.ffprobePath = r.ffprobePath
      this.settings = r.settings
      this.editTranscoding = false
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save transcoding settings'
    }
  }

  startEditSubtitle() {
    this.subtitleInput = this.subtitleLanguage
    this.editSubtitle = true
    this.settingsError = ''
  }

  async saveSubtitle() {
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/subtitle-language', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ language: this.subtitleInput.trim() }),
      })
      this.subtitleLanguage = r.subtitleLanguage
      this.settings = r.settings
      this.editSubtitle = false
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save the subtitle language'
    }
  }

  startEditOptimizer() {
    this.optimizeModeInput = this.optimizeMode
    this.editOptimizer = true
    this.settingsError = ''
  }

  async saveOptimizer() {
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/optimizer', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ mode: this.optimizeModeInput }),
      })
      this.optimizeMode = r.optimizeMode
      this.settings = r.settings
      this.editOptimizer = false
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save the optimizer mode'
    }
  }

  startEditDiscovery() {
    this.discoveryIntervalInput = this.discoveryInterval
    this.editDiscovery = true
    this.settingsError = ''
  }

  async saveDiscovery() {
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/discovery', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ interval: Number(this.discoveryIntervalInput) }),
      })
      this.discoveryInterval = r.discoveryInterval
      this.settings = r.settings
      this.editDiscovery = false
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save the discovery interval'
    }
  }

  async runDiscovery() {
    this.discoveryRunning = true
    this.discoveryMsg = ''
    try {
      await api('/api/admin/discovery/run', { method: 'POST' })
      this.discoveryMsg = 'Discovery sweep started; results appear as it runs.'
    } catch (e) {
      this.discoveryMsg = (await errText(e)) || 'Could not start a discovery sweep'
    } finally {
      this.discoveryRunning = false
    }
  }

  async loadHealth() {
    try {
      this.health = await api('/api/admin/health')
    } catch {
      this.health = null
    }
  }

  async openImportFolderBrowser() {
    this.ifBrowseOpen = true
    await this.importFolderNavigate(this.importFolder || '')
  }

  async importFolderNavigate(path) {
    this.ifError = ''
    try {
      const r = await api('/api/admin/browse' + (path ? '?path=' + encodeURIComponent(path) : ''))
      this.ifPath = r.path
      this.ifParent = r.parent
      this.ifEntries = r.entries
    } catch {
      this.ifError = 'Cannot open that folder'
    }
  }

  async selectImportFolder() {
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/import-folder', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ path: this.ifPath }),
      })
      this.importFolder = r.importFolder
      this.settings = r.settings
      this.ifBrowseOpen = false
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save the import folder'
    }
  }

  async loadSettings() {
    try {
      const r = await api('/api/admin/settings')
      this.mediaFormat = r.mediaFormat
      this.importFolder = r.importFolder
      this.omdbKey = r.omdbKey
      this.logLevel = r.logLevel
      this.logOutput = r.logOutput
      this.transcodeEnabled = r.transcodeEnabled
      this.ffmpegPath = r.ffmpegPath
      this.ffprobePath = r.ffprobePath
      this.subtitleLanguage = r.subtitleLanguage
      this.optimizeMode = r.optimizeMode
      this.discoveryInterval = r.discoveryInterval
      this.settings = r.settings
    } catch {}
  }

  openSettings() {
    this.go('/admin/settings')
  }

  async rebuildDb() {
    if (!confirm('Flush the cache and rebuild it from the data folder? This also clears any pending imports.')) return
    this.rebuilding = true
    this.rebuildMsg = ''
    this.settingsError = ''
    try {
      const r = await api('/api/admin/rebuild', { method: 'POST' })
      this.rebuildMsg = `Rebuilt ${r.categories} categor${r.categories === 1 ? 'y' : 'ies'} and ${r.media} media item${r.media === 1 ? '' : 's'}.`
      await this.loadSettings()
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Rebuild failed'
    } finally {
      this.rebuilding = false
    }
  }

  async optimizeScan() {
    this.optimizeScanning = true
    this.optimizeScanMsg = ''
    this.settingsError = ''
    try {
      const r = await api('/api/admin/optimize/scan', { method: 'POST' })
      this.optimizeScanMsg = `Found ${r.candidates} file${r.candidates === 1 ? '' : 's'} to optimize; ${r.pending} waiting in line.`
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Optimizer scan failed'
    } finally {
      this.optimizeScanning = false
    }
  }

  async enrichScan() {
    this.enrichScanning = true
    this.enrichScanMsg = ''
    this.settingsError = ''
    try {
      const r = await api('/api/admin/enrich/scan', { method: 'POST' })
      this.enrichScanMsg = `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for enrichment; ${r.pending} waiting in line.`
    } catch (e) {
      this.settingsError = (await errText(e)) || 'OMDB enrichment scan failed'
    } finally {
      this.enrichScanning = false
    }
  }

  async thumbnailScan() {
    this.thumbnailScanning = true
    this.thumbnailScanMsg = ''
    this.settingsError = ''
    try {
      const r = await api('/api/admin/thumbnail/scan', { method: 'POST' })
      this.thumbnailScanMsg = `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for thumbnails; ${r.pending} waiting in line.`
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Thumbnail scan failed'
    } finally {
      this.thumbnailScanning = false
    }
  }

  async probeScan() {
    this.probeScanning = true
    this.probeScanMsg = ''
    this.settingsError = ''
    try {
      const r = await api('/api/admin/probe/scan', { method: 'POST' })
      this.probeScanMsg = `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for probing; ${r.pending} waiting in line.`
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Probe scan failed'
    } finally {
      this.probeScanning = false
    }
  }

  async selectFormat() {
    if (!this.formatChoice) return
    this.settingsError = ''
    try {
      const r = await api('/api/admin/settings/format', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ format: this.formatChoice }),
      })
      this.mediaFormat = r.mediaFormat
      this.settings = r.settings
    } catch (e) {
      this.settingsError = (await errText(e)) || 'Could not save the format'
    }
  }

  // --- admin categories ---

  async addCategory() {
    this.catError = ''
    try {
      await api('/api/admin/categories', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ name: this.catName.trim(), alias: this.catAlias.trim(), parentId: this.catParentId }),
      })
      this.catName = ''
      this.catAlias = ''
      this.catParentId = 0
      await this.loadCategories()
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not add category'
    }
  }

  startEditAlias(c) {
    this.editName = c.name
    this.editAlias = c.alias
    this.catError = ''
  }

  async saveAlias() {
    try {
      // Preserve the category's current other-media flag: the PUT carries both fields, so
      // omitting it would clear the flag whenever an alias is edited.
      const other = this.categories.find((c) => c.name === this.editName)?.otherMedia ?? false
      await api('/api/admin/categories/' + encodeURIComponent(this.editName), {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ alias: this.editAlias.trim(), otherMedia: other }),
      })
      this.editName = ''
      await this.loadCategories()
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not update alias'
    }
  }

  // toggleOtherMedia flips a category's other-media flag immediately (no Edit/Save), via
  // the same extended alias endpoint so alias and flag stay in one PUT.
  async toggleOtherMedia(c, value) {
    try {
      await api('/api/admin/categories/' + encodeURIComponent(c.name), {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ alias: c.alias, otherMedia: value }),
      })
      await this.loadCategories()
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not update category'
    }
  }

  categoryAlias(name) {
    return this.categories.find((c) => c.name === name)?.alias ?? name
  }

  // reorderCategory moves the dragged category to the dropped-on target's slot, but only
  // when both share a parent: ordering is per sibling group, so a drop onto another level is
  // ignored. It renumbers that group and persists the new order; the server re-validates.
  async reorderCategory(target) {
    const dragged = this.categories.find((c) => c.name === this.dragName)
    this.dragName = ''
    if (!dragged || !target || dragged.name === target.name) return
    if ((dragged.parentId || 0) !== (target.parentId || 0)) return
    const siblings = this.categories
      .filter((c) => (c.parentId || 0) === (dragged.parentId || 0))
      .sort((a, b) => (a.position ?? 0) - (b.position ?? 0) || (a.leaf ?? a.name).localeCompare(b.leaf ?? b.name))
    const rest = siblings.filter((c) => c.name !== dragged.name)
    const at = rest.findIndex((c) => c.name === target.name)
    rest.splice(at, 0, dragged)
    try {
      await api('/api/admin/categories/reorder', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ parentId: dragged.parentId || 0, order: rest.map((c) => c.id) }),
      })
      await this.loadCategories()
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not reorder categories'
    }
  }

  async deleteCategory(name) {
    if (!confirm(`Delete the empty category "${name}"?`)) return
    this.catError = ''
    try {
      await api('/api/admin/categories/' + encodeURIComponent(name), { method: 'DELETE' })
      await this.loadCategories()
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not delete category'
    }
  }

  // --- admin import ---

  async startImport() {
    this.uploadFiles = []
    this.uploadProgress = []
    this.uploadError = ''
    // The import working view is its own URL so a reload/back lands back here; push it
    // directly rather than via go() so route() does not reset to the resume path and
    // skip the fresh scan below.
    history.pushState({}, '', '/admin/library/import')
    if (this.importSource === 'upload') {
      this.importOrigin = 'upload'
      this.importPage = 'upload'
      return
    }
    if (this.importSource === 'plex') {
      this.importOrigin = 'plex'
      this.importPage = 'plex'
      await this.openPlexImport()
      return
    }
    if (this.importSource === 'jellyfin') {
      this.importOrigin = 'jellyfin'
      this.importPage = 'jellyfin'
      this.openJellyfinImport()
      return
    }
    this.importOrigin = 'folder'
    this.importPage = 'assess'
    await this.runAssess()
  }

  // loadPendingImports refreshes the set of staged-but-not-started (preCheck) rows.
  async loadPendingImports() {
    try {
      this.pendingImports = await api('/api/admin/imports?status=preCheck')
    } catch {
      this.pendingImports = []
    }
  }

  // continueImport resumes a prepared import: it loads the existing preCheck rows
  // straight into the assessment table without re-scanning, so the earlier work
  // (titles, posters, subtitles) is preserved.
  continueImport() {
    if (this.pendingImports.length === 0) return
    this.importCategory = this.pendingImports[0].category
    // Every row carries its producing source in `origin`, so a resumed import shows the
    // right assessment affordances (uploads lock cleanup on, Plex locks it off).
    this.importOrigin = this.pendingImports[0].origin || 'folder'
    if (this.importOrigin === 'upload') this.deleteAfter = true
    if (this.importOrigin === 'plex' || this.importOrigin === 'jellyfin') this.deleteAfter = false
    this.assessRows = this.pendingImports
    this.assessError = ''
    this.assessLoading = false
    this.importPage = 'assess'
  }

  async runAssess() {
    this.assessError = ''
    this.assessLoading = true
    this.assessRows = []
    const cat = this.categories.find((c) => c.name === this.importCategory)
    try {
      this.assessRows = await api('/api/admin/import/assess', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ categoryId: cat?.id ?? 0 }),
      })
    } catch (e) {
      this.assessError = (await errText(e)) || 'Could not assess the import folder'
    } finally {
      this.assessLoading = false
    }
  }

  startEditImport(group) {
    this.editKey = group.key
    this.editTitle = group.title
    this.editYear = String(group.year || '')
    this.editCategory = group.category
  }

  // An edit re-titles and can re-categorise every file of the media (the whole show),
  // so all member rows update together and share the refreshed OMDb result.
  async saveImportEdit() {
    const group = this.assessGroups.find((g) => g.key === this.editKey)
    if (!group) {
      this.editKey = ''
      return
    }
    const yearStr = this.editYear.trim()
    if (yearStr !== '' && !/^\d+$/.test(yearStr)) {
      this.assessError = 'Year must be a number'
      return
    }
    const year = yearStr === '' ? 0 : Number(yearStr)
    const title = this.editTitle.trim()
    const categoryId = this.categories.find((c) => c.name === this.editCategory)?.id ?? 0
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
      this.assessRows = this.assessRows.map((r) => byId.get(r.id) || r)
      this.editKey = ''
      this.assessError = ''
    } catch (e) {
      this.assessError = (await errText(e)) || 'Could not update the row'
    }
  }

  async deleteImportRow(group) {
    try {
      await Promise.all(group.ids.map((id) => api('/api/admin/imports/' + id, { method: 'DELETE' })))
      const gone = new Set(group.ids)
      this.assessRows = this.assessRows.filter((r) => !gone.has(r.id))
    } catch {}
  }

  async startImportBatch() {
    try {
      await api('/api/admin/import/start', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          deleteAfter:
            this.importOrigin === 'upload'
              ? true
              : this.importOrigin === 'plex' || this.importOrigin === 'jellyfin'
                ? false
                : this.deleteAfter,
        }),
      })
      this.importPage = ''
      this.pendingImports = []
      this.go('/admin/progress')
    } catch (e) {
      this.assessError = (await errText(e)) || 'Could not start the import'
    }
  }

  onUploadPick(e) {
    this.uploadFiles = [...e.target.files]
    this.uploadProgress = this.uploadFiles.map((f) => ({ name: f.name, pct: 0, status: 'pending' }))
    this.uploadError = ''
  }

  // startUpload opens a session, uploads every picked file (one progress bar each), then
  // stages them into the same assessment table the folder import uses.
  async startUpload() {
    if (!this.uploadFiles.length || this.uploading) return
    this.uploading = true
    this.uploadError = ''
    try {
      const { session } = await api('/api/admin/import/upload/begin', { method: 'POST' })
      for (let i = 0; i < this.uploadFiles.length; i++) {
        this.uploadProgress[i].status = 'up'
        try {
          await uploadFile(this.uploadFiles[i], session, (pct) => (this.uploadProgress[i].pct = pct))
          this.uploadProgress[i].pct = 100
          this.uploadProgress[i].status = 'done'
        } catch (err) {
          this.uploadProgress[i].status = 'error'
          throw err
        }
      }
      const cat = this.categories.find((c) => c.name === this.importCategory)
      this.assessRows = await api('/api/admin/import/upload/assess', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ session, categoryId: cat?.id ?? 0 }),
      })
      this.deleteAfter = true
      this.importPage = 'assess'
    } catch (e) {
      this.uploadError = (await errText(e)) || e?.message || 'Upload failed'
    } finally {
      this.uploading = false
    }
  }

  // --- admin import: Plex source ---

  async openPlexImport() {
    this.plexError = ''
    this.plexChecked = false
    this.plexRows = []
    this.plexProgress = null
    this.plexStaging = false
    this.plexMetaDir = ''
    this.plexBrowseOpen = false
    try {
      const r = await api('/api/admin/import/plex/default')
      this.plexDB = r.dbPath || ''
    } catch {
      this.plexDB = ''
    }
  }

  async plexCheck() {
    if (!this.plexDB.trim()) return
    this.plexError = ''
    this.plexChecking = true
    this.plexChecked = false
    try {
      const libs = await api('/api/admin/import/plex/check', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ dbPath: this.plexDB.trim(), metadataDir: this.plexMetaDir.trim() }),
      })
      this.plexRows = (libs || []).map((l) => ({
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
      this.plexChecked = true
    } catch (e) {
      this.plexError = (await errText(e)) || 'Could not open the Plex database'
    } finally {
      this.plexChecking = false
    }
  }

  async plexBrowseNavigate(path) {
    this.plexBrowseError = ''
    try {
      const q = '?files=true' + (path ? '&path=' + encodeURIComponent(path) : '')
      this.plexBrowse = await api('/api/admin/browse' + q)
    } catch {
      this.plexBrowseError = 'Cannot open that folder'
    }
  }

  async openPlexBrowse() {
    this.plexBrowseOpen = true
    await this.plexBrowseNavigate('')
  }

  pickPlexDB(entry) {
    if (entry.isDir) {
      this.plexBrowseNavigate(entry.path)
      return
    }
    this.plexDB = entry.path
    this.plexBrowseOpen = false
  }

  // togglePlexRow flips a library's selection; selecting one resolves its paths.
  async togglePlexRow(row) {
    row.selected = !row.selected
    if (row.selected && row.status === '') await this.plexResolveRow(row)
  }

  // plexResolveRow re-checks one library's path status, applying its media-location
  // override when given.
  async plexResolveRow(row) {
    row.resolving = true
    try {
      const res = await api('/api/admin/import/plex/resolve', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          dbPath: this.plexDB.trim(),
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
      this.plexError = (await errText(e)) || 'Could not resolve paths'
    } finally {
      row.resolving = false
    }
  }

  async startPlexStaging() {
    if (!this.plexReady || this.plexStaging) return
    const chosen = this.plexRows.filter((r) => r.selected)
    this.plexError = ''
    try {
      await api('/api/admin/import/plex/prepare', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          dbPath: this.plexDB.trim(),
          metadataDir: this.plexMetaDir.trim(),
          selections: chosen.map((r) => ({
            section: r.section,
            categoryId: r.categoryId,
            create: r.categoryId === 0,
          })),
          remaps: chosen.map((r) => ({ section: r.section, from: r.from, to: r.to })),
        }),
      })
      this.plexStaging = true
      this.plexProgress = { total: 0, done: 0, staged: 0, missing: 0, finished: false, error: '' }
      this.pollPlexProgress()
    } catch (e) {
      this.plexError = (await errText(e)) || 'Could not start staging'
    }
  }

  pollPlexProgress() {
    clearInterval(this.plexTimer)
    this.plexTimer = setInterval(async () => {
      try {
        const p = await api('/api/admin/import/plex/progress')
        this.plexProgress = p
        if (p.finished) {
          clearInterval(this.plexTimer)
          this.plexTimer = 0
          this.plexStaging = false
          if (p.error) {
            this.plexError = p.error
          } else {
            await this.loadPendingImports()
            this.go('/admin/library/import')
          }
        }
      } catch {
        clearInterval(this.plexTimer)
        this.plexTimer = 0
        this.plexStaging = false
      }
    }, 1000)
  }

  // --- admin import: Jellyfin (NFO) source ---

  openJellyfinImport() {
    this.jellyfinDir = ''
    this.jellyfinError = ''
    this.jellyfinCategoryId = this.categories[0]?.id ?? 0
    this.jellyfinNewName = ''
    this.jellyfinProgress = null
    this.jellyfinStaging = false
    this.jellyfinBrowseOpen = false
  }

  async jellyfinBrowseNavigate(path) {
    this.jellyfinBrowseError = ''
    try {
      const q = path ? '?path=' + encodeURIComponent(path) : ''
      this.jellyfinBrowse = await api('/api/admin/browse' + q)
    } catch {
      this.jellyfinBrowseError = 'Cannot open that folder'
    }
  }

  async openJellyfinBrowse() {
    this.jellyfinBrowseOpen = true
    await this.jellyfinBrowseNavigate('')
  }

  selectJellyfinDir() {
    this.jellyfinDir = this.jellyfinBrowse.path
    this.jellyfinBrowseOpen = false
  }

  async startJellyfinStaging() {
    if (!this.jellyfinReady || this.jellyfinStaging) return
    this.jellyfinError = ''
    try {
      await api('/api/admin/import/jellyfin/prepare', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          sourceDir: this.jellyfinDir.trim(),
          categoryId: this.jellyfinCategoryId,
          create: this.jellyfinCategoryId === 0,
          category: this.jellyfinNewName.trim(),
        }),
      })
      this.jellyfinStaging = true
      this.jellyfinProgress = { total: 0, done: 0, staged: 0, missing: 0, finished: false, error: '' }
      this.pollJellyfinProgress()
    } catch (e) {
      this.jellyfinError = (await errText(e)) || 'Could not start staging'
    }
  }

  pollJellyfinProgress() {
    clearInterval(this.jellyfinTimer)
    this.jellyfinTimer = setInterval(async () => {
      try {
        const p = await api('/api/admin/import/jellyfin/progress')
        this.jellyfinProgress = p
        if (p.finished) {
          clearInterval(this.jellyfinTimer)
          this.jellyfinTimer = 0
          this.jellyfinStaging = false
          if (p.error) {
            this.jellyfinError = p.error
          } else {
            await this.loadPendingImports()
            this.go('/admin/library/import')
          }
        }
      } catch {
        clearInterval(this.jellyfinTimer)
        this.jellyfinTimer = 0
        this.jellyfinStaging = false
      }
    }, 1000)
  }

  // --- admin progress ---

  async loadProgress() {
    try {
      this.progressRows = await api('/api/admin/imports/active')
    } catch {
      this.progressRows = []
    }
    try {
      const r = await api('/api/admin/optimize/active')
      this.optimizeRows = r.active
      this.optimizePending = r.pending
    } catch {
      this.optimizeRows = []
      this.optimizePending = 0
    }
    try {
      const r = await api('/api/admin/enrich/active')
      this.enrichRows = r.active
      this.enrichPending = r.pending
    } catch {
      this.enrichRows = []
      this.enrichPending = 0
    }
    try {
      const r = await api('/api/admin/thumbnail/active')
      this.thumbnailRows = r.active
      this.thumbnailPending = r.pending
    } catch {
      this.thumbnailRows = []
      this.thumbnailPending = 0
    }
    try {
      const r = await api('/api/admin/probe/active')
      this.probeRows = r.active
      this.probePending = r.pending
    } catch {
      this.probeRows = []
      this.probePending = 0
    }
  }

  startProgressPoll() {
    this.loadProgress()
    this.stopProgressPoll()
    this.progressTimer = setInterval(() => this.loadProgress(), 1000)
  }

  stopProgressPoll() {
    if (this.progressTimer) {
      clearInterval(this.progressTimer)
      this.progressTimer = 0
    }
  }

  // --- admin dashboard ---

  async loadSummary() {
    try {
      this.summary = await api('/api/admin/summary')
    } catch {
      this.summary = null
    }
    this.loadHealth()
  }

  // --- admin users ---

  async loadUsers() {
    this.usersError = ''
    try {
      this.users = await api('/api/admin/users')
    } catch (e) {
      this.users = []
      this.usersError = (await errText(e)) || 'Could not load users'
    }
  }

  async addUser() {
    this.usersError = ''
    try {
      await api('/api/admin/users', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          email: this.newUserEmail.trim(),
          alias: this.newUserAlias.trim(),
          password: this.newUserPassword,
          admin: this.newUserAdmin,
        }),
      })
      this.newUserEmail = ''
      this.newUserAlias = ''
      this.newUserPassword = ''
      this.newUserAdmin = false
      await this.loadUsers()
    } catch (e) {
      this.usersError = (await errText(e)) || 'Could not create user'
    }
  }

  async patchUser(u, body) {
    this.usersError = ''
    try {
      await api('/api/admin/users/' + u.id, {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      })
      await this.loadUsers()
    } catch (e) {
      this.usersError = (await errText(e)) || 'Could not update user'
    }
  }

  startEditUser(u) {
    this.editUserId = u.id
    this.editUserAlias = u.alias
  }

  async saveUserAlias(u) {
    await this.patchUser(u, { alias: this.editUserAlias.trim() })
    this.editUserId = 0
  }
}
