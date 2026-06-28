<script>
  import MediaTile from '../../components/MediaTile.svelte'

  // One home section: a single row of tiles by default, with a trailing "+N more" tile that
  // expands the section to the full wrapping grid on click. The column count is derived from
  // the measured grid width so the row always fills exactly one line at any viewport size.
  let { title, items, onRemove = null } = $props()

  const TILE_MIN = 150 // keep in sync with .poster-grid minmax() in app.css
  const GAP = 16 // 1rem

  let width = $state(0)
  let expanded = $state(false)

  // Columns the auto-fill grid creates at this width; until measured, assume everything fits
  // (no premature collapse on first paint).
  const cols = $derived(width > 0 ? Math.max(1, Math.floor((width + GAP) / (TILE_MIN + GAP))) : items.length || 1)
  const overflow = $derived(!expanded && items.length > cols)
  const visible = $derived(overflow ? items.slice(0, cols - 1) : items)
  const moreCount = $derived(items.length - (cols - 1))
</script>

{#if items.length}
  <div class="ff-row-head">
    <h2 class="title is-5 ff-row-title">{title}</h2>
    {#if expanded && items.length > cols}
      <a href={null} class="ff-row-toggle" onclick={() => (expanded = false)}>Show less</a>
    {/if}
  </div>
  <div class="poster-grid" bind:clientWidth={width}>
    {#each visible as m (m.id)}
      <MediaTile {m} {onRemove} />
    {/each}
    {#if overflow}
      <div class="poster-tile">
        <button class="poster-card ff-more-card" onclick={() => (expanded = true)} title="Show all {items.length}">
          <span class="ff-more-count">+{moreCount}</span>
          <span class="ff-more-label">more</span>
        </button>
      </div>
    {/if}
  </div>
{/if}
