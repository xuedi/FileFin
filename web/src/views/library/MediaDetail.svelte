<script>
  import { getContext, tick } from 'svelte'
  import { episodeLabel } from '../../lib/app.svelte.js'
  import Player from './Player.svelte'
  const app = getContext('app')

  // The inline tag editor is local to this page: only the stored list lives on AppState.
  let tagInputOpen = $state(false)
  let newTag = $state('')
  let tagInputEl = $state(null)

  async function openTagInput() {
    tagInputOpen = true
    await tick()
    tagInputEl?.focus()
  }

  function closeTagInput() {
    tagInputOpen = false
    newTag = ''
  }

  // Enter commits and keeps the field open for the next tag; Escape abandons it.
  function onTagKey(e) {
    if (e.key === 'Enter') {
      e.preventDefault()
      app.addTag(newTag)
      newTag = ''
    } else if (e.key === 'Escape') {
      closeTagInput()
    }
  }
</script>

{#if app.detail}
  {@const detail = app.detail}
  <button class="button is-ghost is-small ff-back" onclick={() => history.back()}>&larr; Back</button>
  <div class="ff-detail">
    <div class="ff-detail-main">
      <div class="ff-titlebar">
        <h2 class="title is-4">
          {detail.title}
          <span class="has-text-grey has-text-weight-normal">(<a
              href={null}
              class="ff-pivot"
              onclick={() => app.go('/search?field=year&q=' + detail.year)}>{detail.year}</a>)</span>
          {#if detail.watched}<span class="tag is-success is-light">&#10003; Watched</span>{/if}
        </h2>
        {#if !app.playing}
          <div class="ff-title-actions">
            <div class="select ff-rating" title="Your rating">
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
            {#if app.me?.admin}
              <button class="button" title="Edit this item's metadata and poster" onclick={() => app.goEditMeta(detail.id)}>
                Edit
              </button>
            {/if}
          </div>
        {/if}
      </div>

      {#if app.playing}
        <Player />
      {/if}

      {#if detail.description}<p>{detail.description}</p>{/if}
      {#if detail.genres.length}
        <div class="tags ff-tags">
          {#each detail.genres as g}
            <a href={null} class="tag" onclick={() => app.go('/search?field=genre&q=' + encodeURIComponent(g))}>{g}</a>
          {/each}
        </div>
      {/if}
      {#if detail.tags.length || app.me?.admin}
        <div class="tags ff-tags ff-usertags">
          {#each detail.tags as t}
            <span class="tag is-link is-light">
              <a href={null} onclick={() => app.goTag(t)}>{t}</a>
              {#if app.me?.admin}
                <button class="delete is-small" aria-label={'Remove tag ' + t} onclick={() => app.removeTag(t)}></button>
              {/if}
            </span>
          {/each}
          {#if app.me?.admin}
            {#if tagInputOpen}
              <input
                class="input is-small ff-tag-input"
                list="ff-tag-vocab"
                placeholder="tag, then Enter"
                bind:value={newTag}
                bind:this={tagInputEl}
                onkeydown={onTagKey}
                onblur={closeTagInput} />
              <datalist id="ff-tag-vocab">
                {#each app.tags as t}<option value={t.tag}></option>{/each}
              </datalist>
            {:else}
              <button class="tag ff-tag-add" title="Add a tag" onclick={openTagInput}>+ tag</button>
            {/if}
          {/if}
        </div>
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
          {#each detail.metadata as m}
            <tr>
              <th>{m.key}</th>
              <td>
                {#if m.key === 'Directed by'}
                  <a href={null} class="ff-pivot" onclick={() => app.go('/search?field=director&q=' + encodeURIComponent(m.value))}>{m.value}</a>
                {:else if m.key === 'Language'}
                  <a href={null} class="ff-pivot" onclick={() => app.go('/search?field=language&q=' + encodeURIComponent(m.value))}>{m.value}</a>
                {:else}
                  {m.value}
                {/if}
              </td>
            </tr>
          {/each}
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
        <ul class="ff-cast">
          {#each detail.actors as a}
            <li><a href={null} class="ff-pivot" onclick={() => app.go('/search?field=cast&q=' + encodeURIComponent(a))}>{a}</a></li>
          {/each}
        </ul>
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
