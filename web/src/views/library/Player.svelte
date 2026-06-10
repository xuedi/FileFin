<script>
  import { getContext } from 'svelte'
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
          if (Hls.isSupported()) {
            hls = new Hls()
            hls.loadSource(url)
            hls.attachMedia(el)
          } else {
            el.src = url
          }
        })
      }
    }

    // Subtitles are independent of the stream, so native <track>s work for both
    // direct-play and HLS. Clear any from the previous file, then add this file's.
    el.querySelectorAll('track').forEach((t) => t.remove())
    for (const sub of f?.subtitles ?? []) {
      const track = document.createElement('track')
      track.kind = 'subtitles'
      track.srclang = sub.lang
      track.label = sub.label || sub.lang
      track.src = base + '/sub/' + sub.index
      el.appendChild(track)
    }

    const onMeta = () => {
      if (seekTo > 0 && el && el.currentTime < seekTo) el.currentTime = seekTo
    }
    let lastMark = 0
    const onTime = () => {
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
        el.querySelectorAll('track').forEach((t) => t.remove())
      }
      if (hls) {
        hls.destroy()
        hls = null
      }
    }
  })
</script>

<video class="ff-video-player" controls autoplay bind:this={app.videoEl}></video>
