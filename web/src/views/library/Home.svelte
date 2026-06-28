<script>
  import { getContext } from 'svelte'
  import MediaTile from '../../components/MediaTile.svelte'
  import SearchBar from './SearchBar.svelte'
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
  <h2 class="title is-5 ff-row-title">Continue watching</h2>
  {#if app.homeData.continue.length}
    <div class="poster-grid">
      {#each app.homeData.continue as m}<MediaTile {m} onRemove={(x) => app.removeFromContinue(x)} />{/each}
    </div>
  {:else}
    <p class="has-text-grey has-text-centered ff-loading">Nothing in progress - pick a category to start watching.</p>
  {/if}
  {#if app.homeData.favorites.length}
    <h2 class="title is-5 ff-row-title">Favorites</h2>
    <div class="poster-grid">
      {#each app.homeData.favorites as m}<MediaTile {m} onRemove={(x) => app.removeFromFavorites(x)} />{/each}
    </div>
  {/if}
  {#if app.homeData.completed.length}
    <h2 class="title is-5 ff-row-title">Completed</h2>
    <div class="poster-grid">
      {#each app.homeData.completed as m}<MediaTile {m} onRemove={(x) => app.removeFromCompleted(x)} />{/each}
    </div>
  {/if}
{/if}
