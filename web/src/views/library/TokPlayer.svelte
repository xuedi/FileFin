<script>
  import { getContext } from 'svelte'
  const app = getContext('app')

  // Wire the TokTok player whenever the current item changes (same direct-play vs HLS
  // decision as the detail player); on 'ended' it auto-advances to the next video.
  $effect(() => {
    if (!app.tokOn || !app.tokVideoEl || !app.tokCurrent) return
    const el = app.tokVideoEl
    const { mediaId, file, transcode, subtitles } = app.tokCurrent
    const base = '/api/media/' + mediaId + '/file/' + file
    let cancelled = false
    if (!transcode) {
      el.src = base
    } else {
      const url = base + '/hls/index.m3u8'
      if (el.canPlayType('application/vnd.apple.mpegurl')) {
        el.src = url
      } else {
        import('hls.js').then(({ default: Hls }) => {
          if (cancelled || !el) return
          if (Hls.isSupported()) {
            app.tokHls = new Hls()
            app.tokHls.loadSource(url)
            app.tokHls.attachMedia(el)
          } else {
            el.src = url
          }
        })
      }
    }
    el.querySelectorAll('track').forEach((t) => t.remove())
    for (const sub of subtitles) {
      const track = document.createElement('track')
      track.kind = 'subtitles'
      track.srclang = sub.lang
      track.label = sub.label || sub.lang
      track.src = base + '/sub/' + sub.index
      el.appendChild(track)
    }
    el.play?.().catch(() => {})

    let lastMark = 0
    const onTime = () => {
      if (el && Math.abs(el.currentTime - lastMark) >= 30) {
        lastMark = el.currentTime
        app.reportProgress(mediaId, file, 'checkpoint', false, el)
      }
    }
    const onEnded = () => {
      app.reportProgress(mediaId, file, 'ended', false, el)
      app.advanceTok()
    }
    el.addEventListener('timeupdate', onTime)
    el.addEventListener('ended', onEnded)
    return () => {
      cancelled = true
      if (el) {
        el.removeEventListener('timeupdate', onTime)
        el.removeEventListener('ended', onEnded)
        el.querySelectorAll('track').forEach((t) => t.remove())
      }
      if (app.tokHls) {
        app.tokHls.destroy()
        app.tokHls = null
      }
    }
  })
</script>

<div class="tok">
  <div class="tok-bar">
    <span class="tok-title">{app.tokTitle}</span>
    <div class="tok-actions">
      <button class="tok-btn" title="Next video" onclick={() => app.advanceTok()}>&#9197;</button>
      <button class="tok-btn" title="Close (Esc)" onclick={() => app.stopTokTok()}>&#10005;</button>
    </div>
  </div>
  <video class="tok-video" controls autoplay playsinline bind:this={app.tokVideoEl}></video>
</div>
