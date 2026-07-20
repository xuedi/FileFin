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

const sidebarKey = 'ff-sidebar-open'

// readSidebarOpen restores which library sidebar accordions were left open. Categories
// default to open (the browse axis that always exists), tags to closed.
function readSidebarOpen() {
  const fallback = { categories: true, tags: false }
  try {
    return { ...fallback, ...JSON.parse(localStorage.getItem(sidebarKey) || '{}') }
  } catch {
    return fallback
  }
}

// humanDuration renders a span of seconds as a short "2h 5m" / "5m 10s" / "10s" label.
function humanDuration(secs) {
  secs = Math.max(0, Math.floor(secs))
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  const s = secs % 60
  if (h) return `${h}h ${m}m`
  if (m) return `${m}m ${s}s`
  return `${s}s`
}

// humanSize renders a byte count as a short "8.2 GB" / "740 MB" label (decimal units, the
// ones disk sizes are sold in).
export function humanSize(bytes) {
  if (!bytes || bytes < 0) return '-'
  const units = ['B', 'kB', 'MB', 'GB', 'TB']
  let n = bytes
  let u = 0
  while (n >= 1000 && u < units.length - 1) {
    n /= 1000
    u++
  }
  return `${u === 0 || n >= 100 ? Math.round(n) : n.toFixed(1)} ${units[u]}`
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
  version = $state('') // running binary's release version, from /api/state
  me = $state(null) // { user, admin }
  view = $state('library') // 'library' | 'admin' | 'settings'
  adminView = $state('dashboard') // 'dashboard' | 'stats' | 'library' | 'import' | 'users' | 'settings' | 'progress' | 'unhealthy'
  userMenuOpen = $state(false) // navbar username dropdown

  // user settings: MyDramaList / MyAnimeList import (username, optional category scope,
  // in-flight flags, last preview)
  mdl = $state({ username: '', categoryId: 0, loading: false, applying: false, preview: null })
  mal = $state({ username: '', categoryId: 0, loading: false, applying: false, preview: null })

  // admin: unhealthy media (metadata matching). The list of unmatched items, plus a drill-in
  // detail with an editable title/year/IMDb-id form and the OMDb candidates it searches up.
  // misfiled holds the items whose looked-up language/country contradicts their category.
  unhealthy = $state({
    items: [], loading: false, detailId: '', detail: null, misfiled: [],
    form: { title: '', year: '', imdbId: '' }, candidates: null, searching: false, applying: false,
  })

  // admin: metadata editor (library detail "Edit" -> /media/{id}/edit). The raw editable
  // meta.json fields, the folder facts shown alongside, and in-flight flags. posterVersion
  // bumps to bust the poster preview cache after an upload.
  edit = $state({
    id: '', folder: '', category: '', hasPoster: false, posterVersion: 0,
    loading: false, saving: false, uploadingPoster: false, form: null,
  })

  // install form. The port is fixed at bootstrap (not entered here); the one-time setup token
  // arrives in the install URL and is held in memory, sent as a header on every setup call.
  iuser = $state('')
  ipass = $state('')
  setupToken = $state('')
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

  // The library sidebar groups its two browse axes into accordions: the category tree and
  // the curated tag vocabulary ([{ tag, count }], newest counts from /api/tags).
  tags = $state([])
  sidebarOpen = $state(readSidebarOpen())

  // admin tag manager: the vocabulary by name, plus the row being renamed in place
  adminTags = $state([])
  tagEdit = $state('') // the tag whose rename input is open; '' = none
  tagEditValue = $state('')
  tagBusy = $state(false)

  // library views: home grids, category grid, media detail + player
  libMode = $state('home') // 'home' | 'category' | 'detail' | 'search'
  homeData = $state({ continue: [], favorites: [], completed: [] })

  // library search: the active query, its field scope, and the result rows. The search
  // section lives on the home page; 'search' mode hides the three home lists for the grid.
  searchQuery = $state('')
  searchField = $state('all')
  searchResults = $state([])
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
  // The category page: catPage is the relpath being edited ('' = the list), catDetail what
  // the server said about it, and catForm the working copy the form binds to.
  catPage = $state('')
  catDetail = $state(null)
  catForm = $state(null)
  catSaving = $state(false)

  // admin settings. The editable fields below are the working copy the per-section forms
  // bind to; settingsBaseline holds the last-saved values so each tab knows when it is dirty.
  // sys* are read-only install facts shown in the System tab.
  settingsTab = $state('system') // system | library | playback | automation | logging | maintenance
  sysPort = $state(0)
  sysDataDir = $state('')
  sysCachePath = $state('')
  sysUsers = $state(0)
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
  discoveryInterval = $state(0)
  discoveryNextRun = $state(0) // unix seconds of the next scheduled discovery sweep (0 = off)
  nowTs = $state(0) // wall clock (ms), ticked while on Settings, for the discovery countdown
  settingsClock = 0 // interval id for the countdown ticker
  tasks = $state(null) // { imports, optimize, enrich, thumbnail, probe } backlog, or null
  settingsBaseline = $state({}) // snapshot of saved editable values, for dirty detection
  formatChoice = $state('')

  optimizeModes = [
    { value: 'none', label: 'Off' },
    { value: 'cpu', label: 'CPU only' },
    { value: 'gpu', label: 'GPU only' },
    { value: 'all', label: 'GPU + CPU' },
  ]
  discoveryIntervals = [
    { value: 0, label: 'Off' },
    { value: 3600, label: 'Every 1 hour' },
    { value: 10800, label: 'Every 3 hours' },
    { value: 43200, label: 'Every 12 hours' },
    { value: 86400, label: 'Every 24 hours' },
  ]

  discoveryRunning = $state(false)
  health = $state(null) // { items: [{id, title, issues:[{code,detail}], lastChecked}] }

  // import-folder picker (settings)
  ifBrowseOpen = $state(false)
  ifPath = $state('')
  ifParent = $state('')
  ifEntries = $state([])
  ifError = $state('')

  // toasts: transient success/error notices, replacing the old inline status lines
  toasts = $state([])
  _toastSeq = 0

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

  // rebuild + background scans (results surface as toasts)
  rebuilding = $state(false)
  rebuildProgress = $state(null) // { total, done, categories, media, finished, error } while a rebuild runs
  rebuildTimer = 0
  optimizeScanning = $state(false)
  enrichScanning = $state(false)
  thumbnailScanning = $state(false)
  probeScanning = $state(false)

  // admin import
  importCategory = $state('')
  importPage = $state('') // '' = the import page, else 'assess' | 'upload' | 'plex' | 'jellyfin'
  // The import page's table: one row per recognised media in the import folder, each with its
  // own edited title/year and target category, plus the folder they were read from. That path
  // is kept separate from the Settings field of the same name so an unsaved edit there never
  // decides what this page reads.
  importFolderPath = $state('')
  importItems = $state([])
  importSkipped = $state([]) // rows taken off the table with X, offered back under the button
  importScanning = $state(false)
  importScanError = $state('')

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
  // "clean up also non imported media": empty the whole import folder once the batch has
  // copied. It rides on deleteAfter, so it is forced off whenever the originals are kept.
  purgeFolder = $state(false)

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

  // admin statistics
  stats = $state(null)

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
        g = {
          key, title: r.title, year: r.year, category: r.category,
          ids: [], count: 0, hasPoster: false, subCount: 0, duplicate: '',
        }
        map.set(key, g)
      }
      g.ids.push(r.id)
      g.count++
      if (r.hasPoster) g.hasPoster = true
      g.subCount += r.subCount || 0
      if (r.duplicate && !g.duplicate) g.duplicate = r.duplicate
    }
    return [...map.values()]
  })

  // How many staged items the library already holds, so the preCheck page can warn once
  // above the button instead of relying on the per-row markers being spotted.
  assessDuplicates = $derived(this.assessGroups.filter((g) => g.duplicate).length)

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

  // captureSetupToken reads the one-time token from the install URL's ?token=..., keeps it in
  // memory, and scrubs it from the address bar (and browser history) so it is never re-sent in
  // a query string or left visible - every setup call carries it as the X-Setup-Token header.
  captureSetupToken() {
    const t = new URLSearchParams(location.search).get('token')
    if (t) {
      this.setupToken = t
      history.replaceState({}, '', location.pathname)
    }
  }

  // setupHeaders is the header set for a token-gated setup call, merged with any extras.
  setupHeaders(extra = {}) {
    return this.setupToken ? { 'X-Setup-Token': this.setupToken, ...extra } : { ...extra }
  }

  async openBrowser() {
    this.browseOpen = true
    await this.navigate('') // empty path: server starts at the app's current directory
  }

  async navigate(path) {
    this.browseError = ''
    try {
      const q = path ? '?path=' + encodeURIComponent(path) : ''
      const r = await api('/api/install/browse' + q, { headers: this.setupHeaders() })
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
    if (!this.setupToken) {
      this.installError = 'No setup token - open the link printed by `filefin setup`.'
      return
    }
    try {
      const r = await api('/api/install', {
        method: 'POST',
        headers: this.setupHeaders({ 'content-type': 'application/json' }),
        body: JSON.stringify({ username: this.iuser, password: this.ipass, dataDir: this.dataDir }),
      })
      // Setup is complete: the installer is now gone. Reload on the (unchanged) port to land on
      // the login page, without the token in the URL.
      const url = window.location.protocol + '//' + window.location.hostname + ':' + r.port + '/'
      setTimeout(() => {
        window.location.href = url
      }, 800)
    } catch (e) {
      this.installError = (await errText(e)) || 'Setup failed'
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
    this.userMenuOpen = false
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

  // --- tags ---

  async loadTags() {
    try {
      this.tags = await api('/api/tags')
    } catch {
      this.tags = []
    }
  }

  toggleSidebar(section) {
    this.sidebarOpen = { ...this.sidebarOpen, [section]: !this.sidebarOpen[section] }
    try {
      localStorage.setItem(sidebarKey, JSON.stringify(this.sidebarOpen))
    } catch {}
  }

  // goTag pivots into a tag-scoped search, so one grid renders every way of browsing.
  goTag(tag) {
    this.go('/search?field=tag&q=' + encodeURIComponent(tag))
  }

  // activeTag is the tag the current view is filtered by, or '' - used to mark the sidebar
  // entry active without a separate route.
  get activeTag() {
    return this.libMode === 'search' && this.searchField === 'tag' ? this.searchQuery : ''
  }

  // saveTags replaces the open item's curated tags. The server normalises (trim, lowercase,
  // dedupe) and returns the stored list, so the chips always show what is actually on disk.
  async saveTags(tags) {
    if (!this.detail) return
    try {
      const r = await api('/api/admin/media/' + this.detail.id + '/tags', {
        method: 'POST',
        body: JSON.stringify({ tags }),
      })
      this.detail = { ...this.detail, tags: r.tags }
      await this.loadTags() // the sidebar counts follow immediately
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save the tags')
    }
  }

  addTag(tag) {
    const t = (tag || '').trim().toLowerCase()
    if (!t || (this.detail?.tags ?? []).includes(t)) return
    return this.saveTags([...(this.detail?.tags ?? []), t])
  }

  removeTag(tag) {
    return this.saveTags((this.detail?.tags ?? []).filter((t) => t !== tag))
  }

  // --- admin tag manager ---

  async loadAdminTags() {
    this.tagBusy = true
    try {
      this.adminTags = await api('/api/admin/tags')
    } catch {
      this.adminTags = []
    } finally {
      this.tagBusy = false
    }
  }

  startTagEdit(tag) {
    this.tagEdit = tag
    this.tagEditValue = tag
  }

  cancelTagEdit() {
    this.tagEdit = ''
    this.tagEditValue = ''
  }

  // renameTag doubles as the merge: renaming onto an existing tag folds the two together,
  // because each item's list is deduplicated on the way in.
  async renameTag() {
    const from = this.tagEdit
    const to = (this.tagEditValue || '').trim().toLowerCase()
    if (!from || !to || from === to) return this.cancelTagEdit()
    this.tagBusy = true
    try {
      const r = await api('/api/admin/tags/' + encodeURIComponent(from), {
        method: 'PUT',
        body: JSON.stringify({ tag: to }),
      })
      this.toast('success', `Renamed "${from}" to "${to}" on ${r.changed} item${r.changed === 1 ? '' : 's'}.`)
      this.cancelTagEdit()
      await Promise.all([this.loadAdminTags(), this.loadTags()])
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not rename the tag')
    } finally {
      this.tagBusy = false
    }
  }

  async deleteTag(tag) {
    this.tagBusy = true
    try {
      const r = await api('/api/admin/tags/' + encodeURIComponent(tag), { method: 'DELETE' })
      this.toast('success', `Removed "${tag}" from ${r.changed} item${r.changed === 1 ? '' : 's'}.`)
      await Promise.all([this.loadAdminTags(), this.loadTags()])
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not delete the tag')
    } finally {
      this.tagBusy = false
    }
  }

  // categoryIsOtherMedia reports whether the open category page is an other-media category,
  // gating the TokTok button. The flag is set (via the admin checkbox) only on a top-level
  // category and inherited by its sub-categories, so walk up the parent chain: the category
  // counts when its own flag or any ancestor's is set.
  get categoryIsOtherMedia() {
    const byId = new Map(this.homeCategories.map((c) => [c.id, c]))
    let c = this.homeCategories.find((x) => x.name === this.homeCategory)
    while (c) {
      if (c.otherMedia) return true
      c = c.parentId ? byId.get(c.parentId) : null
    }
    return false
  }

  // --- library ---

  async loadHome() {
    try {
      this.homeData = await api('/api/home')
    } catch {
      this.homeData = { continue: [], favorites: [], completed: [] }
    }
  }

  async loadSearch() {
    try {
      this.searchResults = await api(
        '/api/search?q=' + encodeURIComponent(this.searchQuery) + '&field=' + encodeURIComponent(this.searchField),
      )
    } catch {
      this.searchResults = []
    }
  }

  // runSearch navigates to the results URL; route() then parses it and loads the rows.
  // An empty query is a no-op so a bare Enter never replaces the home lists with nothing.
  runSearch(q = this.searchQuery, field = this.searchField) {
    const query = (q || '').trim()
    if (!query) return
    this.go('/search?field=' + encodeURIComponent(field) + '&q=' + encodeURIComponent(query))
  }

  clearSearch() {
    this.go('/')
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

  // --- admin: metadata editor ---

  goEditMeta(id) {
    this.go('/media/' + id + '/edit')
  }

  // loadEdit fetches one item's raw editable metadata and seeds the form. Actors are edited
  // one per line, genres and tags as comma lists, matching how they are joined for display.
  async loadEdit(id) {
    this.edit.id = id
    this.edit.loading = true
    this.edit.form = null
    try {
      const c = await api('/api/admin/media/' + id + '/meta')
      this.edit.folder = c.folder
      this.edit.category = c.category
      this.edit.hasPoster = c.hasPoster
      this.edit.posterVersion = 0
      this.edit.form = {
        title: c.title || '', year: c.year || '',
        description: c.description || '', plot: c.plot || '',
        release: c.release || '', runtime: c.runtime || '',
        language: c.language || '', country: c.country || '',
        director: c.director || '', writer: c.writer || '',
        contentRating: c.contentRating || '', awards: c.awards || '',
        boxOffice: c.boxOffice || '', imdbId: c.imdbId || '',
        imdb: c.imdb || '', rottenTomatoes: c.rottenTomatoes || '', metacritic: c.metacritic || '',
        actors: (c.actors || []).join('\n'),
        genres: (c.genres || []).join(', '),
        tags: (c.tags || []).join(', '),
      }
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not load that item')
      this.go('/media/' + id)
    } finally {
      this.edit.loading = false
    }
  }

  // saveEdit posts the edited fields (a replace-mode write server-side) and lands on the
  // freshly written detail page.
  async saveEdit() {
    const f = this.edit.form
    if (!f) return
    this.edit.saving = true
    try {
      await api('/api/admin/media/' + this.edit.id + '/meta', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          title: f.title.trim(), year: Number(f.year) || 0,
          description: f.description, plot: f.plot,
          release: f.release, runtime: f.runtime, language: f.language, country: f.country,
          director: f.director, writer: f.writer, contentRating: f.contentRating,
          awards: f.awards, boxOffice: f.boxOffice, imdbId: f.imdbId,
          imdb: f.imdb, rottenTomatoes: f.rottenTomatoes, metacritic: f.metacritic,
          actors: f.actors.split('\n').map((s) => s.trim()).filter(Boolean),
          genres: f.genres.split(',').map((s) => s.trim()).filter(Boolean),
          tags: f.tags.split(',').map((s) => s.trim()).filter(Boolean),
        }),
      })
      this.toast('success', 'Saved changes to "' + (f.title.trim() || 'this item') + '".')
      this.go('/media/' + this.edit.id)
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save the changes')
    } finally {
      this.edit.saving = false
    }
  }

  // uploadPoster replaces the base poster from a picked file; the server drops the stale
  // sized variants so the thumbnail agent rebuilds them.
  async uploadPoster(file) {
    if (!file) return
    this.edit.uploadingPoster = true
    try {
      const form = new FormData()
      form.append('poster', file, file.name)
      await api('/api/admin/media/' + this.edit.id + '/poster', { method: 'POST', body: form })
      this.edit.hasPoster = true
      this.edit.posterVersion++
      this.toast('success', 'Poster updated.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not upload the poster')
    } finally {
      this.edit.uploadingPoster = false
    }
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

  async setRating(n) {
    const prev = this.detail.rating
    this.detail.rating = n // optimistic
    try {
      await api('/api/media/' + this.detail.id + '/rating', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ rating: n }),
      })
    } catch {
      this.detail.rating = prev // revert on failure
    }
  }

  // --- MyDramaList import (user settings) ---

  async saveMDLUsername() {
    const name = this.mdl.username.trim()
    try {
      const r = await api('/api/profile/mdl', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ mdlUsername: name }),
      })
      if (this.me) this.me.mdlUsername = r.mdlUsername
      this.mdl.username = r.mdlUsername
      this.toast('success', name ? 'MyDramaList username saved.' : 'MyDramaList username cleared.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save the username')
    }
  }

  async mdlPreview() {
    this.mdl.loading = true
    this.mdl.preview = null
    try {
      const p = await api('/api/mdl/preview', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ categoryId: Number(this.mdl.categoryId) }),
      })
      // Exact and confident (a unique title trusted despite an off/absent year) matches are
      // pre-selected; weaker approximate rows arrive unchecked so a wrong cross-title match is
      // never applied without a deliberate tick.
      p.matched = p.matched.map((m) => ({ ...m, selected: m.confidence === 'exact' || m.confidence === 'confident' }))
      this.mdl.preview = p
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not read your MyDramaList list')
    } finally {
      this.mdl.loading = false
    }
  }

  async mdlApply() {
    await this._watchApply('/api/mdl/apply', this.mdl, 'ratings')
  }

  // --- MyAnimeList import (user settings) ---

  async saveMALUsername() {
    const name = this.mal.username.trim()
    try {
      const r = await api('/api/profile/mal', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ malUsername: name }),
      })
      if (this.me) this.me.malUsername = r.malUsername
      this.mal.username = r.malUsername
      this.toast('success', name ? 'MyAnimeList username saved.' : 'MyAnimeList username cleared.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save the username')
    }
  }

  async malPreview() {
    this.mal.loading = true
    this.mal.preview = null
    try {
      const p = await api('/api/mal/preview', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ categoryId: Number(this.mal.categoryId) }),
      })
      p.matched = p.matched.map((m) => ({ ...m, selected: m.confidence === 'exact' || m.confidence === 'confident' }))
      this.mal.preview = p
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not read your MyAnimeList list')
    } finally {
      this.mal.loading = false
    }
  }

  async malApply() {
    await this._watchApply('/api/mal/apply', this.mal, 'ratings')
  }

  // _watchApply posts the confirmed rows of a watch-history preview (shared by MDL and MAL).
  async _watchApply(path, store, noun) {
    const rows = (store.preview?.matched ?? []).filter((m) => m.selected)
    if (!rows.length) {
      this.toast('error', 'Nothing selected to import.')
      return
    }
    store.applying = true
    try {
      const r = await api(path, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          items: rows.map((m) => ({ mediaId: m.mediaId, rating: m.rating, markWatched: m.willMarkWatched })),
        }),
      })
      store.preview = null
      this.toast('success', 'Imported ' + r.applied + ' item' + (r.applied === 1 ? '' : 's') + (r.failed ? ', ' + r.failed + ' failed' : '') + '.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not import ' + noun)
    } finally {
      store.applying = false
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
      const page = ['dashboard', 'stats', 'library', 'tags', 'import', 'users', 'settings', 'progress', 'unhealthy'].includes(segs[1]) ? segs[1] : 'dashboard'
      this.applyAdmin(page, segs[2])
      return
    }
    if (segs[0] === 'settings') {
      this.view = 'settings'
      this.playing = false
      return
    }
    // Library (also the fallback for non-admins hitting /admin).
    this.view = 'library'
    await Promise.all([this.loadHomeCategories(), this.loadTags()])
    this.playing = false // a route change tears the player down (its effect cleanup reports a stop)
    if (segs[0] === 'media' && segs[1]) {
      if (segs[2] === 'edit' && this.me?.admin) {
        this.libMode = 'editMeta'
        this.detail = null
        await this.loadEdit(segs[1])
      } else {
        this.libMode = 'detail'
        await this.showMedia(segs[1])
      }
    } else if (segs[0] === 'category' && segs[1]) {
      const c = this.homeCategories.find((x) => String(x.id) === segs[1])
      this.homeCategory = c ? c.name : ''
      this.libMode = 'category'
      this.detail = null
      await this.loadCategoryMedia(segs[1])
    } else if (segs[0] === 'search') {
      const params = new URLSearchParams(location.search)
      this.searchQuery = params.get('q') || ''
      this.searchField = params.get('field') || 'all'
      this.homeCategory = ''
      this.libMode = 'search'
      this.detail = null
      await this.loadSearch()
    } else {
      this.homeCategory = ''
      this.libMode = 'home'
      this.detail = null
      this.searchQuery = ''
      this.searchField = 'all'
      this.searchResults = []
      await this.loadHome()
    }
  }

  // applyAdmin sets the admin sub-view and loads its data, without touching history.
  // sub is the optional third path segment ("import" resumes a prepared import).
  applyAdmin(page, sub) {
    const prev = this.adminView
    if (page !== 'progress') this.stopProgressPoll() // leaving Progress stops its poller
    if (page !== 'settings') this.stopSettingsClock() // leaving Settings stops its countdown
    this.adminView = page
    if (page === 'library') {
      // A third segment names the category whose own page is open; without one this is the
      // read-only list.
      this.catPage = sub ? decodeURIComponent(sub) : ''
      this.loadCategories()
      if (this.catPage) this.loadCategory(this.catPage)
      else this.catDetail = this.catForm = null
    } else if (page === 'import') {
      clearInterval(this.plexTimer) // stop any orphaned Plex staging poll
      this.plexTimer = 0
      clearInterval(this.jellyfinTimer) // stop any orphaned Jellyfin staging poll
      this.jellyfinTimer = 0
      this.importPage = ''
      this.loadCategories()
      this.loadImportItems()
      this.loadPendingImports().then(() => {
        if (sub === 'work' && this.pendingImports.length > 0) this.continueImport()
      })
    } else if (page === 'settings') {
      const tabs = ['system', 'library', 'playback', 'automation', 'logging', 'maintenance']
      this.settingsTab = tabs.includes(sub) ? sub : 'system'
      this.startSettingsClock()
      // Switching tabs (already on Settings, with a tab segment) keeps the working copies and
      // any unsaved edits; a fresh entry from the sidebar (no tab segment) reloads from server.
      if (prev !== 'settings' || !sub) {
        this.formatChoice = ''
        this.ifBrowseOpen = false
        this.loadSettings()
      }
    } else if (page === 'tags') {
      this.tagEdit = ''
      this.tagEditValue = ''
      this.loadAdminTags()
    } else if (page === 'users') {
      this.editUserId = 0
      this.loadUsers()
    } else if (page === 'dashboard') {
      this.summary = null
      this.loadSummary()
    } else if (page === 'stats') {
      this.stats = null
      this.loadStats()
    } else if (page === 'progress') {
      this.startProgressPoll()
    } else if (page === 'unhealthy') {
      this.openUnhealthy(sub)
    }
  }

  showLibrary() {
    this.go('/')
  }

  goAdmin() {
    this.go('/admin/' + this.adminView)
  }

  goSettings() {
    this.go('/settings')
  }

  // loadScopeCategories populates the category tree for the user-settings import scope
  // dropdown. The import flow is auth-gated (not admin-gated), so it reads the plain
  // /api/categories endpoint, which every signed-in user can reach.
  async loadScopeCategories() {
    try {
      this.categories = await api('/api/categories')
    } catch {
      this.categories = []
    }
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

  openAdminImport() {
    this.go('/admin/import')
  }

  // --- admin settings ---

  // applySettings syncs the working fields and the dirty baseline from a server response,
  // so after a save the edited tab is clean and the System facts stay current.
  applySettings(r) {
    this.sysPort = r.port
    this.sysDataDir = r.dataDir
    this.sysCachePath = r.cachePath
    this.sysUsers = r.users
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
    this.discoveryNextRun = r.discoveryNextRun
    this.settingsBaseline = {
      importFolder: r.importFolder,
      omdbKey: r.omdbKey,
      logLevel: r.logLevel,
      logOutput: r.logOutput,
      transcodeEnabled: r.transcodeEnabled,
      ffmpegPath: r.ffmpegPath,
      ffprobePath: r.ffprobePath,
      subtitleLanguage: r.subtitleLanguage,
      optimizeMode: r.optimizeMode,
      discoveryInterval: r.discoveryInterval,
    }
  }

  // Per-sub-group dirty flags (a sub-group is one POST endpoint); a tab is dirty when any of
  // its sub-groups are. Reading $state here keeps these reactive in the template.
  get importFolderDirty() { return this.importFolder !== this.settingsBaseline.importFolder }
  get omdbDirty() { return this.omdbKey !== this.settingsBaseline.omdbKey }
  get transcodingDirty() {
    const b = this.settingsBaseline
    return this.transcodeEnabled !== b.transcodeEnabled || this.ffmpegPath !== b.ffmpegPath || this.ffprobePath !== b.ffprobePath
  }
  get subtitleDirty() { return this.subtitleLanguage !== this.settingsBaseline.subtitleLanguage }
  get optimizerDirty() { return this.optimizeMode !== this.settingsBaseline.optimizeMode }
  get discoveryDirty() { return Number(this.discoveryInterval) !== this.settingsBaseline.discoveryInterval }
  get loggingDirty() {
    return this.logLevel !== this.settingsBaseline.logLevel || this.logOutput !== this.settingsBaseline.logOutput
  }
  get libraryDirty() { return this.importFolderDirty || this.omdbDirty }
  get playbackDirty() { return this.transcodingDirty || this.subtitleDirty }
  get automationDirty() { return this.optimizerDirty || this.discoveryDirty }

  // resetTab reverts a tab's working fields to the saved baseline.
  resetTab(tab) {
    const b = this.settingsBaseline
    if (tab === 'library') {
      this.importFolder = b.importFolder
      this.omdbKey = b.omdbKey
    } else if (tab === 'playback') {
      this.transcodeEnabled = b.transcodeEnabled
      this.ffmpegPath = b.ffmpegPath
      this.ffprobePath = b.ffprobePath
      this.subtitleLanguage = b.subtitleLanguage
    } else if (tab === 'automation') {
      this.optimizeMode = b.optimizeMode
      this.discoveryInterval = b.discoveryInterval
    } else if (tab === 'logging') {
      this.logLevel = b.logLevel
      this.logOutput = b.logOutput
    }
  }

  // _postSetting POSTs one settings sub-group and applies the fresh server view on success.
  async _postSetting(path, body) {
    this.applySettings(
      await api(path, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(body),
      }),
    )
  }

  async saveLibrary() {
    try {
      if (this.importFolderDirty) await this._postSetting('/api/admin/settings/import-folder', { path: this.importFolder })
      if (this.omdbDirty) await this._postSetting('/api/admin/settings/omdb-key', { key: this.omdbKey.trim() })
      this.toast('success', 'Library settings saved.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save library settings')
    }
  }

  async savePlayback() {
    try {
      if (this.transcodingDirty)
        await this._postSetting('/api/admin/settings/transcoding', {
          ffmpegPath: this.ffmpegPath.trim(),
          ffprobePath: this.ffprobePath.trim(),
          enabled: this.transcodeEnabled,
        })
      if (this.subtitleDirty)
        await this._postSetting('/api/admin/settings/subtitle-language', { language: this.subtitleLanguage.trim() })
      this.toast('success', 'Playback settings saved.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save playback settings')
    }
  }

  async saveAutomation() {
    try {
      if (this.optimizerDirty) await this._postSetting('/api/admin/settings/optimizer', { mode: this.optimizeMode })
      if (this.discoveryDirty) await this._postSetting('/api/admin/settings/discovery', { interval: Number(this.discoveryInterval) })
      this.toast('success', 'Automation settings saved.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save automation settings')
    }
  }

  async saveLogging() {
    try {
      await this._postSetting('/api/admin/settings/logging', { level: this.logLevel, output: this.logOutput.trim() })
      this.toast('success', 'Logging settings saved.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save logging settings')
    }
  }

  // toast queues a transient notice; errors linger a little longer than successes.
  toast(kind, text) {
    const id = ++this._toastSeq
    this.toasts = [...this.toasts, { id, kind, text }]
    setTimeout(() => this.dismissToast(id), kind === 'error' ? 6000 : 3500)
  }

  dismissToast(id) {
    this.toasts = this.toasts.filter((t) => t.id !== id)
  }

  async runDiscovery() {
    this.discoveryRunning = true
    try {
      await api('/api/admin/discovery/run', { method: 'POST' })
      this.toast('success', 'Discovery sweep started; results appear as it runs.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not start a discovery sweep')
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

  // --- admin: unhealthy media (metadata matching) ---

  // openUnhealthy loads the page's data: the disk-health section (read-only) always, then
  // either one item's match context (when a media id is in the URL) or the unmatched list.
  async openUnhealthy(id) {
    this.loadHealth()
    this.unhealthy.detailId = id || ''
    if (id) {
      await this.openUnmatched(id)
    } else {
      this.unhealthy.detail = null
      await this.loadUnmatched()
      this.loadMisfiled()
    }
  }

  // loadMisfiled reads the items whose metadata disagrees with the category they sit in. It
  // is a report only: nothing is moved, here or anywhere.
  async loadMisfiled() {
    try {
      const r = await api('/api/admin/misfiled')
      this.unhealthy.misfiled = r.items || []
    } catch {
      this.unhealthy.misfiled = []
    }
  }

  async loadUnmatched() {
    this.unhealthy.loading = true
    try {
      const r = await api('/api/admin/unmatched')
      this.unhealthy.items = r.items || []
    } catch {
      this.unhealthy.items = []
    } finally {
      this.unhealthy.loading = false
    }
  }

  goUnhealthy(id) {
    this.go('/admin/unhealthy/' + id)
  }

  // openUnmatched loads one item's match context and seeds the search form from its current
  // title/year/IMDb id.
  async openUnmatched(id) {
    this.unhealthy.detail = null
    this.unhealthy.candidates = null
    try {
      const c = await api('/api/admin/media/' + id + '/match')
      this.unhealthy.detail = c
      this.unhealthy.form = { title: c.title || '', year: c.year || '', imdbId: c.imdbId || '' }
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not load that item')
      this.go('/admin/unhealthy')
    }
  }

  // useGuess fills the form from the folder-name guess (title + year).
  useGuess() {
    const d = this.unhealthy.detail
    if (!d) return
    this.unhealthy.form.title = d.guessTitle || ''
    this.unhealthy.form.year = d.guessYear || ''
  }

  async searchOmdb() {
    const d = this.unhealthy.detail
    if (!d) return
    this.unhealthy.searching = true
    this.unhealthy.candidates = null
    try {
      const r = await api('/api/admin/media/' + d.id + '/omdb-search', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          title: this.unhealthy.form.title.trim(),
          year: Number(this.unhealthy.form.year) || 0,
          imdbId: this.unhealthy.form.imdbId.trim(),
        }),
      })
      this.unhealthy.candidates = r.candidates || []
    } catch (e) {
      this.toast('error', (await errText(e)) || 'OMDb search failed')
      this.unhealthy.candidates = []
    } finally {
      this.unhealthy.searching = false
    }
  }

  // applyMatch writes the chosen candidate (classic enrichment, in replace mode) and returns
  // to the list, where the now-matched item no longer appears.
  async applyMatch(cand) {
    const d = this.unhealthy.detail
    if (!d) return
    const title = this.unhealthy.form.title.trim() || cand.title
    this.unhealthy.applying = true
    try {
      await api('/api/admin/media/' + d.id + '/match', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ imdbId: cand.imdbId, title, year: Number(this.unhealthy.form.year) || 0 }),
      })
      this.toast('success', 'Matched "' + title + '".')
      this.go('/media/' + d.id) // land on the freshly written detail page
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not apply the match')
    } finally {
      this.unhealthy.applying = false
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

  // selectImportFolder just updates the Library tab's working copy from the picker; the
  // actual save happens on that tab's Save (so it stays consistent with the dirty model).
  selectImportFolder() {
    this.importFolder = this.ifPath
    this.ifBrowseOpen = false
  }

  async loadSettings() {
    try {
      this.applySettings(await api('/api/admin/settings'))
    } catch {}
  }

  openSettings() {
    this.go('/admin/settings')
  }

  // The countdown clock ticks only while the Settings page is open: every second for the
  // discovery "next run in ..." countdown, and every five seconds it refreshes the task
  // backlog so the System tab's Tasks box stays roughly live without an app-wide timer.
  startSettingsClock() {
    this.nowTs = Date.now()
    this.loadTaskBacklog()
    clearInterval(this.settingsClock)
    let ticks = 0
    this.settingsClock = setInterval(() => {
      this.nowTs = Date.now()
      if (++ticks % 5 === 0) this.loadTaskBacklog()
    }, 1000)
  }

  stopSettingsClock() {
    clearInterval(this.settingsClock)
    this.settingsClock = 0
  }

  async loadTaskBacklog() {
    try {
      this.tasks = await api('/api/admin/tasks')
    } catch {
      this.tasks = null
    }
  }

  // discoveryStatus is the System tab's discovery value: "Off" when disabled, otherwise a
  // live countdown to the next scheduled sweep.
  get discoveryStatus() {
    if (!this.discoveryInterval) return 'Off'
    if (!this.discoveryNextRun) return 'scheduled'
    const remaining = this.discoveryNextRun * 1000 - this.nowTs
    if (remaining <= 0) return 'next run due now'
    return 'next run in ' + humanDuration(remaining / 1000)
  }

  // rebuildDb starts the background rebuild and polls its progress; the danger zone shows a
  // bar until it finishes (the rebuild runs off the request, so the POST returns immediately).
  async rebuildDb() {
    if (!confirm('Flush the cache and rebuild it from the data folder? This also clears any pending imports.')) return
    this.rebuilding = true
    this.rebuildProgress = { total: 0, done: 0, categories: 0, media: 0, finished: false, error: '' }
    try {
      await api('/api/admin/rebuild', { method: 'POST' })
      this.pollRebuildProgress()
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Rebuild failed')
      this.rebuilding = false
      this.rebuildProgress = null
    }
  }

  pollRebuildProgress() {
    clearInterval(this.rebuildTimer)
    this.rebuildTimer = setInterval(async () => {
      try {
        const p = await api('/api/admin/rebuild/progress')
        this.rebuildProgress = p
        if (p.finished) {
          clearInterval(this.rebuildTimer)
          this.rebuildTimer = 0
          this.rebuilding = false
          this.rebuildProgress = null
          if (p.error) {
            this.toast('error', p.error)
          } else {
            this.toast('success', `Rebuilt ${p.categories} categor${p.categories === 1 ? 'y' : 'ies'} and ${p.media} media item${p.media === 1 ? '' : 's'}.`)
            await this.loadSettings()
          }
        }
      } catch {
        clearInterval(this.rebuildTimer)
        this.rebuildTimer = 0
        this.rebuilding = false
        this.rebuildProgress = null
      }
    }, 700)
  }

  async optimizeScan() {
    this.optimizeScanning = true
    try {
      const r = await api('/api/admin/optimize/scan', { method: 'POST' })
      this.toast('success', `Found ${r.candidates} file${r.candidates === 1 ? '' : 's'} to optimize; ${r.pending} waiting in line.`)
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Optimizer scan failed')
    } finally {
      this.optimizeScanning = false
    }
  }

  async enrichScan() {
    this.enrichScanning = true
    try {
      const r = await api('/api/admin/enrich/scan', { method: 'POST' })
      this.toast('success', `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for enrichment; ${r.pending} waiting in line.`)
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Metadata scan failed')
    } finally {
      this.enrichScanning = false
    }
  }

  async thumbnailScan() {
    this.thumbnailScanning = true
    try {
      const r = await api('/api/admin/thumbnail/scan', { method: 'POST' })
      this.toast('success', `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for thumbnails; ${r.pending} waiting in line.`)
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Thumbnail scan failed')
    } finally {
      this.thumbnailScanning = false
    }
  }

  async probeScan() {
    this.probeScanning = true
    try {
      const r = await api('/api/admin/probe/scan', { method: 'POST' })
      this.toast('success', `Queued ${r.candidates} folder${r.candidates === 1 ? '' : 's'} for probing; ${r.pending} waiting in line.`)
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Probe scan failed')
    } finally {
      this.probeScanning = false
    }
  }

  async selectFormat() {
    if (!this.formatChoice) return
    try {
      this.applySettings(
        await api('/api/admin/settings/format', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ format: this.formatChoice }),
        }),
      )
      this.toast('success', 'Media format selected.')
    } catch (e) {
      this.toast('error', (await errText(e)) || 'Could not save the format')
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

  // openCategory leaves the list for one category's own page, where everything about it is
  // edited. The name is a relpath, so it is encoded into the single URL segment.
  openCategory(c) {
    this.go('/admin/library/' + encodeURIComponent(c.name))
  }

  // loadCategory reads one category's page payload and derives the working copy the form
  // binds to. The keyword-style lists edit as comma-separated text, which is how an admin
  // thinks of them, and are split again on save.
  async loadCategory(name) {
    this.catError = ''
    this.catDetail = null
    this.catForm = null
    try {
      const d = await api('/api/admin/categories/' + encodeURIComponent(name))
      this.catDetail = d
      this.catForm = {
        alias: d.alias,
        otherMedia: d.otherMedia,
        kind: d.markers.kind || 'both',
        languages: (d.markers.languages || []).join(', '),
        countries: (d.markers.countries || []).join(', '),
        keywords: (d.markers.keywords || []).join(', '),
      }
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not load the category'
    }
  }

  // saveCategory writes the whole category in one PUT: identity and markers together. The
  // learned markers ride along unchanged unless a row was removed, which is what makes the
  // page the single write path.
  async saveCategory(learned) {
    if (!this.catDetail || !this.catForm) return
    const f = this.catForm
    const list = (s) => s.split(',').map((x) => x.trim()).filter(Boolean)
    const markers = {
      kind: f.kind,
      languages: list(f.languages),
      countries: list(f.countries),
      keywords: list(f.keywords),
    }
    if (learned) markers.learned = learned
    this.catSaving = true
    this.catError = ''
    try {
      await api('/api/admin/categories/' + encodeURIComponent(this.catDetail.name), {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ alias: f.alias.trim(), otherMedia: f.otherMedia, markers }),
      })
      await this.loadCategory(this.catDetail.name)
      await this.loadCategories()
      this.toast('success', 'Category saved.')
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not save the category'
    } finally {
      this.catSaving = false
    }
  }

  // removeLearnedMarker prunes one marker the imports got wrong: the surviving counts are
  // sent back as the new learned set.
  async removeLearnedMarker(marker) {
    if (!this.catDetail) return
    const learned = {}
    for (const row of this.catDetail.learned) {
      if (row.marker !== marker) learned[row.marker] = row.count
    }
    await this.saveCategory(learned)
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
      if (this.catPage === name) this.go('/admin/library') // its own page is gone with it
    } catch (e) {
      this.catError = (await errText(e)) || 'Could not delete category'
    }
  }

  // --- admin import ---

  // loadImportItems reads the import folder: one row per recognised media, with the title,
  // year, and duplicate check the import would use. Each row keeps its own editable copy of
  // the title/year and its own target category, so the page needs no global picker.
  async loadImportItems() {
    this.importScanError = ''
    this.importScanning = true
    try {
      const r = await api('/api/admin/import/folder')
      this.importFolderPath = r.folder || ''
      const fallback = this.categories.find((c) => c.name === this.importCategory)?.id ?? this.categories[0]?.id ?? 0
      // Least trustworthy first: those are the rows worth reading before pressing Import.
      const rank = { low: 0, medium: 1, high: 2 }
      this.importItems = (r.items || [])
        .map((it, order) => ({
          ...it,
          order, // scan order, so a row taken back lands where it was
          year: it.year || '',
          // The markers' guess wins when it earned one; below the evidence threshold the row
          // falls back to the plain default and says nothing about why.
          categoryId: it.categoryId || this.categoryForKind(it.isShow) || fallback,
        }))
        .sort((a, b) => (rank[a.confidence] ?? 3) - (rank[b.confidence] ?? 3) || a.order - b.order)
      this.importSkipped = []
    } catch (e) {
      this.importItems = []
      this.importScanError = (await errText(e)) || 'Could not read the import folder'
    } finally {
      this.importScanning = false
    }
  }

  // categoryForKind preselects the category a recognised media most likely belongs to, so a
  // folder of shows and a folder of films do not both need picking by hand. It is only a
  // default - every row keeps its own editable dropdown - and yields nothing when no
  // category reads like the kind in question.
  categoryForKind(isShow) {
    const want = isShow ? /show|serie|tv|drama|anime/i : /film|movie|kino|cinema/i
    return this.categories.find((c) => want.test(c.alias || c.name))?.id
  }

  // dropImportItem takes a row off the table and parks it in the skipped list under the
  // button. Nothing was staged, so this only means "not this time" - the media stays in the
  // import folder either way, and takeBackImportItem puts the row back with its edits intact.
  dropImportItem(item) {
    this.importItems = this.importItems.filter((i) => i.id !== item.id)
    if (!this.importSkipped.some((i) => i.id === item.id)) this.importSkipped.push(item)
  }

  takeBackImportItem(item) {
    this.importSkipped = this.importSkipped.filter((i) => i.id !== item.id)
    this.importItems = [...this.importItems, item].sort((a, b) => a.order - b.order)
  }

  importReady = $derived(this.importItems.length > 0 && this.importItems.every((i) => i.categoryId > 0))

  // startFolderImport queues every listed media with its own title, year, and category. The
  // page the admin just reviewed is the check, so the rows go straight to the copy queue.
  async startFolderImport() {
    if (!this.importReady) return
    this.importScanError = ''
    const items = this.importItems.map((i) => ({
      id: i.id,
      title: i.title,
      year: Number(i.year) || 0,
      categoryId: i.categoryId,
    }))
    try {
      const r = await api('/api/admin/import/folder/start', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          deleteAfter: this.deleteAfter,
          purgeFolder: this.deleteAfter && this.purgeFolder,
          items,
        }),
      })
      this.toast('success', `Queued ${r.started} media file(s) for import.`)
      this.importItems = []
      this.importSkipped = []
      this.go('/admin/progress')
    } catch (e) {
      this.importScanError = (await errText(e)) || 'Could not start the import'
    }
  }

  // openWorkPage moves to the working view of one of the sources that bring their own flow.
  // The view is its own URL so a reload/back lands back on it; the URL is pushed directly
  // rather than through go() so route() does not immediately reset the page it just set.
  openWorkPage(origin) {
    history.pushState({}, '', '/admin/import/work')
    this.importOrigin = origin
    this.importPage = origin
  }

  openUploadImport() {
    this.uploadFiles = []
    this.uploadProgress = []
    this.uploadError = ''
    this.openWorkPage('upload')
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
    this.openWorkPage('plex')
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
            this.go('/admin/import/work')
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
    this.openWorkPage('jellyfin')
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
            this.go('/admin/import/work')
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

  // --- admin statistics ---

  async loadStats() {
    try {
      this.stats = await api('/api/admin/stats')
    } catch {
      this.stats = null
    }
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
