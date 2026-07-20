<script>
  import { getContext } from 'svelte'

  // A poster tile in a grid. onRemove (optional) shows a hover "x"; showWatched renders the
  // watched toggle - green once watched, otherwise a neutral check shown only on hover.
  let { m, onRemove = null, showWatched = false } = $props()
  const app = getContext('app')

  // The card carries the toggle, so it cannot itself be a <button>: an interactive descendant
  // of one is invalid, and the template parser would break the nesting apart.
  function openKey(e) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      app.openMedia(m)
    }
  }
</script>

<div class="poster-tile">
  <div class="poster-card" role="button" tabindex="0" onclick={() => app.openMedia(m)} onkeydown={openKey}>
    <div class="poster">
      {#if m.hasPoster}
        <img src={'/api/media/' + m.id + '/poster?size=tile'} alt={m.title} />
      {:else}
        <div class="noposter">{m.title}</div>
      {/if}
      {#if showWatched}
        <button
          class="poster-watched"
          class:is-watched={m.watched}
          title={m.watched ? 'Mark as unwatched' : 'Mark as watched'}
          onclick={(e) => { e.stopPropagation(); app.toggleWatched(m) }}>&#10003;</button>
      {/if}
    </div>
    <span class="poster-name">{m.title}</span>
    <span class="poster-year">{m.year}</span>
  </div>
  {#if onRemove}
    <button class="tile-remove" title="Remove" onclick={(e) => { e.stopPropagation(); onRemove(m) }}>&#10005;</button>
  {/if}
</div>
