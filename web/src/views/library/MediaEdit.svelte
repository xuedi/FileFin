<script>
  import { getContext } from 'svelte'
  const app = getContext('app')
  const e = $derived(app.edit)

  // The single-line "Details" and "Ratings" fields, driven from one list to keep the markup flat.
  const detailFields = [
    { key: 'release', label: 'Released' },
    { key: 'runtime', label: 'Runtime' },
    { key: 'language', label: 'Language' },
    { key: 'country', label: 'Country' },
    { key: 'director', label: 'Directed by' },
    { key: 'writer', label: 'Written by' },
    { key: 'contentRating', label: 'Rated' },
    { key: 'awards', label: 'Awards' },
    { key: 'boxOffice', label: 'Box office' },
    { key: 'imdbId', label: 'IMDb ID' },
  ]
  const ratingFields = [
    { key: 'imdb', label: 'IMDb' },
    { key: 'rottenTomatoes', label: 'Rotten Tomatoes' },
    { key: 'metacritic', label: 'Metacritic' },
  ]

  function pickPoster(ev) {
    const file = ev.currentTarget.files?.[0]
    if (file) app.uploadPoster(file)
    ev.currentTarget.value = '' // allow re-picking the same file
  }
</script>

<button class="button is-ghost is-small ff-back" onclick={() => app.go('/media/' + e.id)}>&larr; Back to details</button>

{#if e.loading}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{:else if e.form}
  {@const f = e.form}
  <div class="ff-titlebar">
    <div>
      <h2 class="title is-4">Edit metadata</h2>
      <p class="ff-settings-intro has-text-grey">{e.folder}{#if e.category} &middot; in {e.category}{/if}</p>
    </div>
    <div class="ff-title-actions">
      <button class="button" onclick={() => app.goUnhealthy(e.id)} title="Search the online database and pick a match">
        Match with the API
      </button>
      <button class="button is-primary" class:is-loading={e.saving} onclick={() => app.saveEdit()}>Save</button>
    </div>
  </div>

  <div class="ff-detail">
    <div class="ff-detail-main">
      <div class="box ff-settings-card">
        <h3 class="title is-6">Basics</h3>
        <div class="field is-grouped">
          <div class="control is-expanded">
            <label class="label is-small" for="ff-edit-title">Title</label>
            <input id="ff-edit-title" class="input" type="text" bind:value={f.title} />
          </div>
          <div class="control">
            <label class="label is-small" for="ff-edit-year">Year</label>
            <input id="ff-edit-year" class="input ff-match-year" type="number" bind:value={f.year} />
          </div>
        </div>
      </div>

      <div class="box ff-settings-card">
        <h3 class="title is-6">Overview</h3>
        <div class="field">
          <label class="label is-small" for="ff-edit-description">Description</label>
          <textarea id="ff-edit-description" class="textarea" rows="2" bind:value={f.description}></textarea>
        </div>
        <div class="field">
          <label class="label is-small" for="ff-edit-plot">Plot</label>
          <textarea id="ff-edit-plot" class="textarea" rows="4" bind:value={f.plot}></textarea>
        </div>
      </div>

      <div class="box ff-settings-card">
        <h3 class="title is-6">Details</h3>
        <div class="ff-edit-grid">
          {#each detailFields as fld}
            <div class="field">
              <label class="label is-small" for={'ff-edit-' + fld.key}>{fld.label}</label>
              <input id={'ff-edit-' + fld.key} class="input" type="text" bind:value={f[fld.key]} />
            </div>
          {/each}
        </div>
      </div>

      <div class="box ff-settings-card">
        <h3 class="title is-6">Ratings</h3>
        <div class="ff-edit-grid">
          {#each ratingFields as fld}
            <div class="field">
              <label class="label is-small" for={'ff-edit-' + fld.key}>{fld.label}</label>
              <input id={'ff-edit-' + fld.key} class="input" type="text" bind:value={f[fld.key]} />
            </div>
          {/each}
        </div>
      </div>

      <div class="box ff-settings-card">
        <h3 class="title is-6">Cast</h3>
        <div class="field">
          <label class="label is-small" for="ff-edit-actors">Actors (one per line)</label>
          <textarea id="ff-edit-actors" class="textarea" rows="5" bind:value={f.actors}></textarea>
        </div>
      </div>

      <div class="box ff-settings-card">
        <h3 class="title is-6">Genres and tags</h3>
        <div class="field">
          <label class="label is-small" for="ff-edit-genres">Genres (comma separated)</label>
          <input id="ff-edit-genres" class="input" type="text" bind:value={f.genres} />
          <p class="help">Supplied by the metadata source; replaced whenever this item is re-matched.</p>
        </div>
        <div class="field">
          <label class="label is-small" for="ff-edit-tags">Tags (comma separated)</label>
          <input id="ff-edit-tags" class="input" type="text" bind:value={f.tags} />
          <p class="help">Your own classification. Never touched by the metadata agents.</p>
        </div>
      </div>

      <div class="ff-settings-actions">
        <button class="button is-primary" class:is-loading={e.saving} onclick={() => app.saveEdit()}>Save</button>
        <button class="button is-ghost" onclick={() => app.go('/media/' + e.id)}>Cancel</button>
      </div>
    </div>

    <aside class="ff-detail-poster">
      <div class="box ff-settings-card">
        <h3 class="title is-6">Poster</h3>
        {#if e.hasPoster}
          <img class="ff-edit-poster" src={'/api/media/' + e.id + '/poster?size=detail&v=' + e.posterVersion} alt="Poster" />
        {:else}
          <div class="ff-edit-poster ff-candidate-noposter">no poster</div>
        {/if}
        <div class="file is-small mt-3">
          <label class="file-label">
            <input class="file-input" type="file" accept="image/*" disabled={e.uploadingPoster} onchange={pickPoster} />
            <span class="file-cta" class:is-loading={e.uploadingPoster}>
              <span class="file-label">{e.hasPoster ? 'Replace poster' : 'Upload poster'}</span>
            </span>
          </label>
        </div>
      </div>
    </aside>
  </div>
{/if}
