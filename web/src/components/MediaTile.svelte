<script>
  import { getContext } from 'svelte'

  // A poster tile in a grid. onRemove (optional) shows a hover "x"; showWatched marks
  // fully-watched items with a corner check.
  let { m, onRemove = null, showWatched = false } = $props()
  const app = getContext('app')
</script>

<div class="poster-tile">
  <button class="poster-card" onclick={() => app.openMedia(m)}>
    <div class="poster">
      {#if m.hasPoster}
        <img src={'/api/media/' + m.id + '/poster?size=tile'} alt={m.title} />
      {:else}
        <div class="noposter">{m.title}</div>
      {/if}
      {#if showWatched && m.watched}
        <span class="poster-watched" title="Watched">&#10003;</span>
      {/if}
    </div>
    <span class="poster-name">{m.title}</span>
    <span class="poster-year">{m.year}</span>
  </button>
  {#if onRemove}
    <button class="tile-remove" title="Remove" onclick={(e) => { e.stopPropagation(); onRemove(m) }}>&#10005;</button>
  {/if}
</div>
