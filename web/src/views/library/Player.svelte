<script>
  import { getContext, untrack } from 'svelte'
  import { preferredSubtitle } from '../../lib/app.svelte.js'
  import { idleChrome, seekBy, toggleFullscreen } from '../../lib/player.js'
  const app = getContext('app')

  // hls is private to one mounted player instance.
  let hls = null

  // Wire up playback whenever the player appears or the chosen file changes. Direct-play
  // files get a plain src; transcoded files load HLS via the browser's native HLS on
  // Safari, or hls.js (lazily imported, bundled by Vite) elsewhere.
  $effect(() => {
    if (!app.playing || !app.videoEl || !app.detail) return
    const el = app.videoEl
    const mediaId = app.detail.id
    const file = app.currentFile // captured so progress reports name the right file after a switch
    const seekTo = app.pendingSeek
    app.pendingSeek = 0

    const base = '/api/media/' + mediaId + '/file/' + file
    const f = app.detail.files.find((x) => x.index === file)
    let cancelled = false
    if (!f?.transcode) {
      el.src = base
    } else {
      const url = base + '/hls/index.m3u8'
      if (el.canPlayType('application/vnd.apple.mpegurl')) {
        el.src = url
      } else {
        import('hls.js').then(({ default: Hls }) => {
          if (cancelled || !el) return
          if (Hls.isSupported()) startHls(Hls, url, -1)
          else el.src = url
        })
      }
    }

    // Subtitles are independent of the stream, so native <track>s work for both
    // direct-play and HLS. Clear any from the previous file, then add this file's, turning
    // on the first one in the user's remembered language for this item.
    el.querySelectorAll('track').forEach((t) => t.remove())
    let active = untrack(() => app.detail.subtitle) || ''
    for (const sub of f?.subtitles ?? []) {
      const track = document.createElement('track')
      track.kind = 'subtitles'
      track.srclang = sub.lang
      track.label = sub.label || sub.lang
      track.src = base + '/sub/' + sub.index
      el.appendChild(track)
    }
    // Tracks are appended in f.subtitles order, so a track's position names its sidecar.
    const applySubtitle = () => {
      const want = preferredSubtitle(f?.subtitles ?? [], active)
      Array.from(el.textTracks).forEach((t, i) => {
        const mode = i === want ? 'showing' : 'disabled'
        if (t.mode !== mode) t.mode = mode
      })
    }
    applySubtitle()
    // The browser runs its own automatic track selection whenever tracks are added or a
    // source (re)loads - a new file or an HLS recovery - and switches on extra tracks. Until
    // the media has loaded, every change is the browser's and is overridden; after that, a
    // single showing track is the user's pick and is remembered, several mean the browser
    // added one. An episode lacking the language just plays without, keeping the choice.
    let settling = true
    const onSettled = () => {
      settling = false
      applySubtitle()
    }
    const onTrackChange = () => {
      const showing = Array.from(el.textTracks).filter((t) => t.mode === 'showing')
      if (settling || showing.length > 1) {
        applySubtitle()
        return
      }
      const lang = showing.length ? showing[0].language || 'und' : ''
      if (lang === active) return
      active = lang
      app.setSubtitlePref(lang)
    }
    el.textTracks.addEventListener('change', onTrackChange)
    el.addEventListener('loadeddata', onSettled)

    // A fatal hls.js error otherwise stops all loading for good: buffered content still
    // plays but nothing ahead ever arrives. Rebuild from the playlist at the current
    // position, which also recreates a server session reaped during a long pause. The
    // budget refills once playback moves again, so a truly broken stream cannot loop.
    let recoveries = 0
    const startHls = (Hls, url, startPosition) => {
      // hls.js's text controllers disable every subtitle track and wipe its cues on attach,
      // detach and playlist load, which would kill our sidecar <track>s on each recovery.
      // Subtitles never come through the playlist, so they are left out.
      hls = new Hls({
        startPosition,
        subtitleTrackController: null,
        subtitleStreamController: null,
        timelineController: null,
      })
      hls.on(Hls.Events.ERROR, (_, data) => {
        if (!data.fatal || cancelled) return
        if (recoveries >= 3) return
        recoveries++
        settling = true
        if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          hls.recoverMediaError()
          return
        }
        const pos = el.currentTime
        hls.destroy()
        startHls(Hls, url, pos)
      })
      hls.loadSource(url)
      hls.attachMedia(el)
    }

    const onMeta = () => {
      if (seekTo > 0 && el && el.currentTime < seekTo) el.currentTime = seekTo
    }
    let lastMark = 0
    const onTime = () => {
      if (!el.paused && !el.seeking) recoveries = 0
      if (el && Math.abs(el.currentTime - lastMark) >= 30) {
        lastMark = el.currentTime
        app.reportProgress(mediaId, file, 'checkpoint')
      }
    }
    const onPause = () => app.reportProgress(mediaId, file, 'pause')
    const onEnded = () => app.reportProgress(mediaId, file, 'ended')
    el.addEventListener('loadedmetadata', onMeta, { once: true })
    el.addEventListener('timeupdate', onTime)
    el.addEventListener('pause', onPause)
    el.addEventListener('ended', onEnded)

    return () => {
      cancelled = true
      app.reportProgress(mediaId, file, 'stop')
      if (el) {
        el.removeEventListener('timeupdate', onTime)
        el.removeEventListener('pause', onPause)
        el.removeEventListener('ended', onEnded)
        el.textTracks.removeEventListener('change', onTrackChange)
        el.removeEventListener('loadeddata', onSettled)
        el.querySelectorAll('track').forEach((t) => t.remove())
      }
      if (hls) {
        hls.destroy()
        hls = null
      }
    }
  })
</script>

<!-- The browser's own fullscreen button would take the bare video there, leaving the overlay
     behind, so it is swapped for ours, which takes the whole frame. -->
<div class="ff-player-frame" use:idleChrome>
  <video class="ff-video-player" controls controlslist="nofullscreen" autoplay bind:this={app.videoEl}></video>
  <div class="ff-player-overlay">
    {#if app.playlist.length > 1}
      <button class="ff-player-btn" title="Previous episode" disabled={!app.prevFile} onclick={() => app.stepFile(-1)}>&#9198;</button>
    {/if}
    <button class="ff-player-btn" title="Back 10 seconds (←)" onclick={() => seekBy(app.videoEl, -10)}>&#8634; 10</button>
    <button class="ff-player-btn" title="Forward 10 seconds (→)" onclick={() => seekBy(app.videoEl, 10)}>10 &#8635;</button>
    {#if app.playlist.length > 1}
      <button class="ff-player-btn" title="Next episode" disabled={!app.nextFile} onclick={() => app.stepFile(1)}>&#9197;</button>
    {/if}
    <button class="ff-player-btn" title="Fullscreen (f)" onclick={() => toggleFullscreen(app.videoEl)}>&#x26F6;</button>
  </div>
</div>
