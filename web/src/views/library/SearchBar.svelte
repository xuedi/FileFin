<script>
  import { getContext } from 'svelte'
  const app = getContext('app')

  // Field scopes: 'all' searches across every text facet; the rest narrow to one.
  const fields = [
    { value: 'all', label: 'All fields' },
    { value: 'title', label: 'Title' },
    { value: 'description', label: 'Description' },
    { value: 'cast', label: 'Cast' },
    { value: 'genre', label: 'Genre' },
    { value: 'tag', label: 'Tags' },
    { value: 'language', label: 'Language' },
    { value: 'director', label: 'Director' },
    { value: 'writer', label: 'Writer' },
    { value: 'year', label: 'Year' },
    { value: 'decade', label: 'Decade' },
  ]

  // Playback status is one of three exclusive states, so it is a scope, not three toggles.
  // "Unwatched" means never started and never finished, which keeps it disjoint from the
  // home page's "Continue watching" row.
  const statuses = [
    { value: 'any', label: 'Any status' },
    { value: 'unwatched', label: 'Unwatched' },
    { value: 'progress', label: 'In progress' },
    { value: 'watched', label: 'Watched' },
  ]

  const sorts = [
    { value: 'year', label: 'By year' },
    { value: 'title', label: 'By title' },
    { value: 'added', label: 'By date added' },
    { value: 'updated', label: 'By recent activity' },
  ]

  function submit(e) {
    e.preventDefault()
    app.runSearch()
  }

  // A control other than the text field is a complete request on its own, so changing one
  // runs the search straight away rather than waiting for the button.
  function apply() {
    if (app.searchAsks) app.runSearch()
  }

  function toggleFav() {
    app.searchFav = !app.searchFav
    apply()
  }

  function toggleDir() {
    app.searchDesc = !app.searchDesc
    apply()
  }
</script>

<form class="ff-search field has-addons" onsubmit={submit}>
  <div class="control">
    <div class="select">
      <select bind:value={app.searchField} aria-label="Search field">
        {#each fields as f}<option value={f.value}>{f.label}</option>{/each}
      </select>
    </div>
  </div>
  <div class="control is-expanded">
    <input class="input" type="text" placeholder="Search the library" bind:value={app.searchQuery} />
  </div>
  <div class="control">
    <div class="select">
      <select bind:value={app.searchStatus} onchange={apply} aria-label="Watch status">
        {#each statuses as s}<option value={s.value}>{s.label}</option>{/each}
      </select>
    </div>
  </div>
  <div class="control">
    <div class="select">
      <select bind:value={app.searchSort} onchange={apply} aria-label="Sort by">
        {#each sorts as s}<option value={s.value}>{s.label}</option>{/each}
      </select>
    </div>
  </div>
  <div class="control">
    <button
      type="button"
      class="button ff-search-toggle"
      onclick={toggleDir}
      aria-label={app.searchDesc ? 'Descending' : 'Ascending'}
      title={app.searchDesc ? 'Descending' : 'Ascending'}>{app.searchDesc ? '↓' : '↑'}</button>
  </div>
  <div class="control">
    <button
      type="button"
      class="button ff-search-toggle"
      class:is-primary={app.searchFav}
      aria-pressed={app.searchFav}
      onclick={toggleFav}
      title="Favorites only">&#9733;</button>
  </div>
  <div class="control">
    <button type="submit" class="button is-primary">Search</button>
  </div>
  {#if app.libMode === 'search'}
    <div class="control">
      <button type="button" class="button" onclick={() => app.clearSearch()}>Clear</button>
    </div>
  {/if}
</form>
