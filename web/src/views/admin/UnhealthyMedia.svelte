<script>
  import { getContext } from 'svelte'
  import { fmtTime } from '../../lib/app.svelte.js'
  const app = getContext('app')
  const u = $derived(app.unhealthy)
</script>

{#if u.detailId}
  <!-- Detail: fix one item's metadata match. -->
  <button class="button is-ghost is-small ff-back" onclick={() => app.go('/admin/unhealthy')}>&larr; Unhealthy media</button>
  {#if u.detail}
    {@const d = u.detail}
    <h1 class="title is-4">{d.folder}</h1>
    <p class="ff-settings-intro has-text-grey">In {d.category}</p>

    {#if d.error}
      <div class="notification is-warning is-light">
        OMDb could not match this automatically: {d.error}
        {#if d.lastAttempt}
          <p class="is-size-7 mt-2">Last tried {d.lastAttempt}{#if d.nextRetry} &middot; will retry after {d.nextRetry}{/if}</p>
        {/if}
      </div>
    {/if}

    <div class="ff-detail">
      <div class="ff-detail-main">
        {#if d.enriched}
          <div class="box ff-settings-card">
            <h2 class="title is-6">Currently matched</h2>
            <table class="table ff-meta-table"><tbody>
              <tr><th>Title</th><td>{d.title} {#if d.year}({d.year}){/if}</td></tr>
              {#if d.imdbId}<tr><th>IMDb ID</th><td>{d.imdbId}</td></tr>{/if}
              {#if d.plot}<tr><th>Plot</th><td>{d.plot}</td></tr>{/if}
            </tbody></table>
          </div>
        {/if}

        <div class="box ff-settings-card">
          <h2 class="title is-6">Files</h2>
          <table class="table is-fullwidth">
            <thead><tr><th>File</th><th>Season</th><th>Episode</th><th>Type</th></tr></thead>
            <tbody>
              {#each d.files as f}
                <tr>
                  <td>{f.name}</td>
                  <td>{f.season || '-'}</td>
                  <td>{f.episode || '-'}</td>
                  <td class="has-text-grey">{f.ext}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <div class="box ff-settings-card">
          <h2 class="title is-6">Find the right title</h2>
          <div class="field is-grouped ff-match-form">
            <div class="control is-expanded">
              <label class="label is-small" for="ff-match-title">Title</label>
              <input id="ff-match-title" class="input" type="text" bind:value={u.form.title} />
            </div>
            <div class="control">
              <label class="label is-small" for="ff-match-year">Year</label>
              <input id="ff-match-year" class="input ff-match-year" type="number" bind:value={u.form.year} />
            </div>
            <div class="control">
              <label class="label is-small" for="ff-match-imdb">IMDb ID</label>
              <input id="ff-match-imdb" class="input" type="text" placeholder="tt..." bind:value={u.form.imdbId} />
            </div>
          </div>
          <div class="ff-settings-actions">
            {#if d.guessTitle}
              <button class="button is-small" onclick={() => app.useGuess()}>Use folder guess: {d.guessTitle}{d.guessYear ? ' (' + d.guessYear + ')' : ''}</button>
            {/if}
            <button class="button is-link" class:is-loading={u.searching} onclick={() => app.searchOmdb()}>Search OMDb</button>
          </div>
        </div>

        {#if u.candidates !== null}
          {#if u.candidates.length}
            <div class="ff-candidates">
              {#each u.candidates as c}
                <div class="box ff-candidate">
                  {#if c.hasPoster}
                    <img class="ff-candidate-poster" src={'/api/admin/omdb/poster/' + c.imdbId} alt={c.title} />
                  {:else}
                    <div class="ff-candidate-poster ff-candidate-noposter">no poster</div>
                  {/if}
                  <div class="ff-candidate-body">
                    <p class="has-text-weight-semibold">{c.title} {#if c.year}<span class="has-text-grey">({c.year})</span>{/if}</p>
                    <p class="is-size-7 has-text-grey">{c.type} &middot; {c.imdbId}</p>
                    <button class="button is-small is-primary" class:is-loading={u.applying} onclick={() => app.applyMatch(c)}>Use this match</button>
                  </div>
                </div>
              {/each}
            </div>
          {:else}
            <p class="help">No candidates found. Try a different title, drop the year, or paste an IMDb id.</p>
          {/if}
        {/if}
      </div>

      {#if d.hasPoster}
        <aside class="ff-detail-poster">
          <img src={'/api/media/' + d.id + '/poster?size=detail'} alt={d.title} />
        </aside>
      {/if}
    </div>
  {:else}
    <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
  {/if}
{:else}
  <!-- List: everything that has no metadata match yet, plus the disk-health section. -->
  <h1 class="title is-4">Unhealthy media</h1>
  <p class="ff-settings-intro has-text-grey">
    Media with no metadata match yet. Open a row to search the database and pick the right title, or
    click a title to edit its metadata by hand.
  </p>

  {#if u.loading}
    <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
  {:else if u.items.length}
    <table class="table is-fullwidth is-hoverable">
      <thead>
        <tr><th>Folder</th><th>Title</th><th>Year</th><th>Category</th><th>Status</th></tr>
      </thead>
      <tbody>
        {#each u.items as it}
          <tr class="ff-clickable" onclick={() => app.goUnhealthy(it.id)}>
            <td>{it.folder}</td>
            <td>
              <a href={null} class="ff-edit-link" title="Edit metadata" onclick={(e) => { e.stopPropagation(); app.goEditMeta(it.id) }}>{it.title}</a>
            </td>
            <td>{it.year || '-'}</td>
            <td class="has-text-grey">{it.category}</td>
            <td>
              {#if it.status === 'error'}
                <span class="tag is-danger is-light ff-health-tag" title={it.error}>error</span>
                {#if it.lastAttempt}<span class="is-size-7 has-text-grey ml-2">last tried {fmtTime(it.lastAttempt)}</span>{/if}
              {:else}
                <span class="tag is-light ff-health-tag">queued</span>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {:else}
    <p class="help">Every title in the library has a metadata match.</p>
  {/if}

  {#if app.health && app.health.items.length}
    <h2 class="title is-5 ff-health-head">Disk health issues</h2>
    <table class="table is-fullwidth">
      <thead>
        <tr><th>Title</th><th>Issues</th><th>Last checked</th></tr>
      </thead>
      <tbody>
        {#each app.health.items as it}
          <tr>
            <td>{it.title || it.id}</td>
            <td>
              {#each it.issues as iss}
                <span class="tag is-warning ff-health-tag" title={iss.detail}>{iss.code}</span>
              {/each}
            </td>
            <td class="has-text-grey">{it.lastChecked}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
{/if}
