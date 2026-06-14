<script>
  import { getContext } from 'svelte'
  import { episodeLabel } from '../../lib/app.svelte.js'
  import Player from './Player.svelte'
  const app = getContext('app')
</script>

{#if app.detail}
  {@const detail = app.detail}
  <button class="button is-ghost is-small ff-back" onclick={() => history.back()}>&larr; Back</button>
  <div class="ff-detail">
    <div class="ff-detail-main">
      <div class="ff-titlebar">
        <h2 class="title is-4">
          {detail.title} <span class="has-text-grey has-text-weight-normal">({detail.year})</span>
          {#if detail.watched}<span class="tag is-success is-light">&#10003; Watched</span>{/if}
        </h2>
        {#if !app.playing}
          <div class="ff-title-actions">
            <div class="select is-small ff-rating" title="Your rating">
              <select value={detail.rating} onchange={(e) => app.setRating(Number(e.currentTarget.value))}>
                <option value={0}>★ Rate</option>
                {#each [1, 2, 3, 4, 5, 6, 7, 8, 9, 10] as n}<option value={n}>★ {n}</option>{/each}
              </select>
            </div>
            <button
              class="button heart"
              class:on={detail.favorite}
              title={detail.favorite ? 'Remove from favorites' : 'Add to favorites'}
              onclick={() => app.toggleFavorite()}>{detail.favorite ? '♥' : '♡'}</button>
            <button class="button is-primary" onclick={() => app.playFile(app.currentFile)}>
              &#9654; {app.hasResume ? 'Continue' : 'Play'}
            </button>
          </div>
        {/if}
      </div>

      {#if app.playing}
        <Player />
      {/if}

      {#if detail.description}<p>{detail.description}</p>{/if}
      {#if detail.tags.length}
        <div class="tags ff-tags">{#each detail.tags as t}<span class="tag">{t}</span>{/each}</div>
      {/if}

      {#if detail.files.length > 1}
        <h3 class="title is-6">Episodes</h3>
        {#if app.seasons.length > 1}
          <div class="buttons ff-seasons">
            {#each app.seasons as s}
              <button
                class="button is-small"
                class:is-link={s.season === app.currentSeason}
                class:ff-watched={s.watched}
                onclick={() => (app.currentSeason = s.season)}>
                {s.season ? 'Season ' + s.season : 'Episodes'}{s.watched ? ' ✓' : ''}
              </button>
            {/each}
          </div>
        {/if}
        <div class="buttons ff-episodes">
          {#each app.currentEpisodes as f}
            <button
              class="button is-small"
              class:is-link={f.index === app.currentFile}
              class:ff-watched={f.watched}
              onclick={() => app.playFile(f.index)}
              title={f.name}>
              {episodeLabel(f)}
            </button>
          {/each}
        </div>
      {/if}

      {#if detail.metadata.length}
        <table class="table ff-meta-table"><tbody>
          {#each detail.metadata as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
        </tbody></table>
      {/if}

      {#if detail.ratings.length}
        <h3 class="title is-6">Ratings</h3>
        <table class="table ff-meta-table"><tbody>
          {#each detail.ratings as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
        </tbody></table>
      {/if}

      {#if detail.technical.length}
        <h3 class="title is-6">Technical</h3>
        <table class="table ff-meta-table"><tbody>
          {#each detail.technical as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
        </tbody></table>
      {/if}

      {#if detail.actors.length}
        <h3 class="title is-6">Cast</h3>
        <ul class="ff-cast">{#each detail.actors as a}<li>{a}</li>{/each}</ul>
      {/if}

      {#if detail.plot}<h3 class="title is-6">Plot</h3><p>{detail.plot}</p>{/if}
    </div>

    {#if detail.hasPoster}
      <aside class="ff-detail-poster">
        <img src={'/api/media/' + detail.id + '/poster?size=detail'} alt={detail.title} />
      </aside>
    {/if}
  </div>
{:else}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{/if}
