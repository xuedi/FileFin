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
    { value: 'language', label: 'Language' },
    { value: 'director', label: 'Director' },
    { value: 'writer', label: 'Writer' },
    { value: 'year', label: 'Year' },
    { value: 'decade', label: 'Decade' },
  ]

  function submit(e) {
    e.preventDefault()
    app.runSearch(app.searchQuery, app.searchField)
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
    <button type="submit" class="button is-primary">Search</button>
  </div>
  {#if app.libMode === 'search'}
    <div class="control">
      <button type="button" class="button" onclick={() => app.clearSearch()}>Clear</button>
    </div>
  {/if}
</form>
