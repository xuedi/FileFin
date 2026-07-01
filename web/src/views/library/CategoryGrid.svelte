<script>
  import { getContext } from 'svelte'
  import MediaTile from '../../components/MediaTile.svelte'
  const app = getContext('app')
</script>

<div class="ff-cat-head">
  <h1 class="title is-4">{app.homeCategoryAlias(app.homeCategory)}</h1>
  {#if app.categoryMedia.length && app.categoryIsOtherMedia}
    <button class="button is-primary" onclick={() => app.startTokTok()}>&#9654; TokTok Mode</button>
  {/if}
</div>
{#if app.categoryMedia.length}
  <div class="poster-grid">
    {#each app.categoryMedia as m}<MediaTile {m} showWatched />{/each}
  </div>
{:else}
  <p class="has-text-grey has-text-centered ff-loading">No media in this category yet.</p>
{/if}
