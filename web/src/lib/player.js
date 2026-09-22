// Shared behaviour for the two <video> players (media detail and TokTok): seeking, fullscreen
// and hiding the controls while the mouse rests in fullscreen.

// How long the mouse must rest in fullscreen before the controls fade out.
const idleAfter = 2500

export function seekBy(el, seconds) {
  if (!el || !Number.isFinite(el.duration)) return
  el.currentTime = Math.min(el.duration, Math.max(0, el.currentTime + seconds))
}

// toggleFullscreen fullscreens the player's frame rather than the bare <video>, so the
// overlay buttons come along; a video without a frame goes fullscreen on its own.
export function toggleFullscreen(el) {
  if (document.fullscreenElement) {
    document.exitFullscreen?.().catch(() => {})
    return
  }
  const target = el?.closest('.ff-player-frame') ?? el
  target?.requestFullscreen?.().catch(() => {})
}

// idleChrome is a Svelte action for a player frame. Outside fullscreen the browser's own
// controls behave as usual. In fullscreen everything starts hidden - the native bar is
// removed by switching the video's controls attribute off, which the browser cannot keep
// stuck on screen the way it sometimes does with its own fade - and comes back on a real
// mouse move, a click or a touch until the mouse rests again.
export function idleChrome(frame) {
  let timer = null
  const video = () => frame.querySelector('video')
  const isFull = () => {
    const fs = document.fullscreenElement
    return !!fs && (fs === frame || frame.contains(fs))
  }

  const setIdle = (idle) => {
    frame.classList.toggle('ff-idle', idle)
    const v = video()
    if (v) v.controls = !idle
  }
  const wake = () => {
    clearTimeout(timer)
    if (!isFull()) return
    setIdle(false)
    timer = setTimeout(() => setIdle(true), idleAfter)
  }
  // Browsers fire a zero-distance mousemove when the layout changes under a still cursor -
  // entering fullscreen does exactly that - so only a move that went somewhere counts.
  const onMove = (e) => {
    if (e.movementX || e.movementY) wake()
  }
  const onFullscreen = () => {
    clearTimeout(timer)
    setIdle(isFull())
  }

  frame.addEventListener('mousemove', onMove)
  frame.addEventListener('pointerdown', wake)
  document.addEventListener('fullscreenchange', onFullscreen)
  return {
    destroy() {
      clearTimeout(timer)
      frame.removeEventListener('mousemove', onMove)
      frame.removeEventListener('pointerdown', wake)
      document.removeEventListener('fullscreenchange', onFullscreen)
    },
  }
}
