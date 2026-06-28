<script>
  import { getContext } from 'svelte'
  import MediaTile from '../../components/MediaTile.svelte'
  import SearchBar from './SearchBar.svelte'
  import HomeRow from './HomeRow.svelte'
  const app = getContext('app')
</script>

<SearchBar />

{#if app.libMode === 'search'}
  <h2 class="title is-5 ff-row-title">
    Results for "{app.searchQuery}"
    <span class="has-text-grey has-text-weight-normal">({app.searchResults.length})</span>
  </h2>
  {#if app.searchResults.length}
    <div class="poster-grid">
      {#each app.searchResults as m}<MediaTile {m} showWatched />{/each}
    </div>
  {:else}
    <p class="has-text-grey has-text-centered ff-loading">No matches</p>
  {/if}
{:else}
  {#if app.homeData.continue.length}
    <HomeRow title="Continue watching" items={app.homeData.continue} onRemove={(x) => app.removeFromContinue(x)} />
  {:else}
    <h2 class="title is-5 ff-row-title">Continue watching</h2>
    <p class="has-text-grey has-text-centered ff-loading">Nothing in progress - pick a category to start watching.</p>
  {/if}
  <HomeRow title="Favorites" items={app.homeData.favorites} onRemove={(x) => app.removeFromFavorites(x)} />
  <HomeRow title="Completed" items={app.homeData.completed} onRemove={(x) => app.removeFromCompleted(x)} />
{/if}
