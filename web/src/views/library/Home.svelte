<script>
  import { getContext } from 'svelte'
  import MediaTile from '../../components/MediaTile.svelte'
  import SearchBar from './SearchBar.svelte'
  import HomeRow from './HomeRow.svelte'
  const app = getContext('app')

  const statusLabels = { unwatched: 'Unwatched', progress: 'Continue watching', watched: 'Completed' }
  const sortLabels = { year: 'by year', title: 'by title', added: 'by date added', updated: 'by recent activity' }

  // A results grid reached from a home row's "more" tile carries no text, so the heading
  // names the filter instead of showing `Results for ""`.
  const heading = $derived.by(() => {
    if (app.searchQuery) return `Results for "${app.searchQuery}"`
    const parts = []
    if (app.searchFav) parts.push('Favorites')
    if (statusLabels[app.searchStatus]) parts.push(statusLabels[app.searchStatus])
    return parts.length ? parts.join(' - ') : 'All media'
  })

  const ordering = $derived(sortLabels[app.searchSort] + (app.searchDesc ? ', newest first' : ''))
</script>

<SearchBar />

{#if app.libMode === 'search'}
  <h2 class="title is-5 ff-row-title">
    {heading}
    <span class="has-text-grey has-text-weight-normal">({app.searchResults.length}) - {ordering}</span>
  </h2>
  {#if app.searchResults.length}
    <div class="poster-grid">
      {#each app.searchResults as m}<MediaTile {m} showWatched />{/each}
    </div>
  {:else}
    <p class="has-text-grey has-text-centered ff-loading">No matches</p>
  {/if}
{:else}
  {#if app.homeData.continue.items.length}
    <HomeRow
      title="Continue watching"
      items={app.homeData.continue.items}
      total={app.homeData.continue.total}
      search={app.homeData.continue.search}
      onRemove={(x) => app.removeFromContinue(x)} />
  {:else}
    <h2 class="title is-5 ff-row-title">Continue watching</h2>
    <p class="has-text-grey has-text-centered ff-loading">Nothing in progress - pick a category to start watching.</p>
  {/if}
  <HomeRow
    title="Favorites"
    items={app.homeData.favorites.items}
    total={app.homeData.favorites.total}
    search={app.homeData.favorites.search}
    onRemove={(x) => app.removeFromFavorites(x)} />
  <HomeRow
    title="Completed"
    items={app.homeData.completed.items}
    total={app.homeData.completed.total}
    search={app.homeData.completed.search}
    onRemove={(x) => app.removeFromCompleted(x)} />
  <HomeRow
    title="Unwatched"
    items={app.homeData.unwatched.items}
    total={app.homeData.unwatched.total}
    search={app.homeData.unwatched.search} />
  <HomeRow
    title="Recently added"
    items={app.homeData.recent.items}
    total={app.homeData.recent.total}
    search={app.homeData.recent.search} />
{/if}
