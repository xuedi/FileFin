<script>
  // Shared folder picker used by install, the settings import folder, and the Plex/Jellyfin
  // source pickers. Renders as a centered modal over a dimmed backdrop; the caller supplies the
  // current listing and the navigation/select/close behaviour, so the same widget drives
  // directory-only and file-picking flows.
  let {
    title = 'Select a folder',
    path = '',
    parent = '',
    error = '',
    entries = [],
    onUp,
    onEntry,
    onClose,
    entryLabel = (e) => e.name,
    onSelect = null,
    selectLabel = 'Select this folder',
  } = $props()
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onClose?.()} />

<div class="modal is-active">
  <button type="button" class="modal-background" aria-label="Close" onclick={() => onClose?.()}></button>
  <div class="modal-card ff-browser-modal">
    <header class="modal-card-head">
      <p class="modal-card-title">{title}</p>
      <button type="button" class="delete" aria-label="close" onclick={() => onClose?.()}></button>
    </header>
    <section class="modal-card-body">
      <p class="is-size-7 has-text-grey ff-browser-path">{path}</p>
      {#if error}<p class="has-text-danger is-size-7">{error}</p>{/if}
      <ul class="menu-list ff-browser-list">
        {#if parent}
          <li><a href={null} onclick={onUp}>.. (up)</a></li>
        {/if}
        {#each entries as e}
          <li><a href={null} onclick={() => onEntry(e)}>{entryLabel(e)}</a></li>
        {/each}
      </ul>
    </section>
    <footer class="modal-card-foot">
      {#if onSelect}
        <button type="button" class="button is-primary" onclick={onSelect}>{selectLabel}</button>
      {/if}
      <button type="button" class="button" onclick={() => onClose?.()}>Cancel</button>
    </footer>
  </div>
</div>
