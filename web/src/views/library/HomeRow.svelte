<script>
  import { getContext } from 'svelte'
  import MediaTile from '../../components/MediaTile.svelte'

  // One home section: a single row of tiles, with a trailing "+N more" tile that opens the
  // whole list in the search view - the same items in the same order, because the server
  // hands the row the search query string that reproduces it. The column count is derived
  // from the measured grid width so the row always fills exactly one line at any viewport
  // size. `total` is the unclipped count, which is what the "+N" counts against.
  let { title, items, total = items.length, search = '', onRemove = null } = $props()

  const app = getContext('app')

  const TILE_MIN = 150 // keep in sync with .poster-grid minmax() in app.css
  const GAP = 16 // 1rem

  let width = $state(0)

  // Columns the auto-fill grid creates at this width; until measured, assume everything fits
  // (no premature collapse on first paint).
  const cols = $derived(width > 0 ? Math.max(1, Math.floor((width + GAP) / (TILE_MIN + GAP))) : items.length || 1)
  const overflow = $derived(total > cols)
  const visible = $derived(overflow ? items.slice(0, cols - 1) : items)
  const moreCount = $derived(total - (cols - 1))
</script>

{#if items.length}
  <h2 class="title is-5 ff-row-title">{title}</h2>
  <div class="poster-grid" bind:clientWidth={width}>
    {#each visible as m (m.id)}
      <MediaTile {m} {onRemove} />
    {/each}
    {#if overflow}
      <div class="poster-tile">
        <button
          class="poster-card ff-more-card"
          onclick={() => app.go('/search?' + search)}
          title="Show all {total}">
          <span class="ff-more-count">+{moreCount}</span>
          <span class="ff-more-label">more</span>
        </button>
      </div>
    {/if}
  </div>
{/if}
