<script>
  import { getContext } from 'svelte'
  const app = getContext('app')
  const a = $derived(app.attention)
  const rows = $derived(app.attentionRows)

  // The chips, in the order the list is sorted: what is broken first, what is merely
  // suggested last. Each is always shown, so "nothing wrong" reads as a zero rather than
  // as a section that quietly disappeared.
  const chips = [
    { key: 'all', label: 'All' },
    { key: 'disk', label: 'Disk' },
    { key: 'metadata', label: 'No metadata' },
    { key: 'name', label: 'Name' },
    { key: 'category', label: 'Category' },
  ]

  const problemLabel = {
    disk: 'Disk',
    metadata: 'No metadata',
    name: 'Name',
    category: 'Category',
  }

  // What a human has to do about each disk issue. The row already states what is wrong; these
  // say what to do about it, which is the whole reason a disk row is worth showing to a person.
  const issueHelp = {
    meta_missing: 'Edit the item’s metadata to write one, or import the folder again.',
    meta_invalid: 'Repair or delete the file, then run discovery.',
    no_video: 'Put the video back, or delete the folder.',
    file_missing: 'Restore the file, or rebuild the cache without it.',
    file_empty: 'Copy it again from the source; a zero-byte file cannot be played.',
    poster_missing: 'Upload a poster in the metadata editor.',
    orphan_optimized: 'Its source video is gone, so the copy can be deleted by hand.',
    orphan_poster: 'It is rebuilt on its own once the item has a base poster again.',
  }
</script>

{#if a.detailId}
  <!-- Detail: fix one item's metadata match. -->
  <button class="button is-ghost is-small ff-back" onclick={() => app.go('/admin/attention')}>&larr; Needs attention</button>
  {#if a.detail}
    {@const d = a.detail}
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
              <input id="ff-match-title" class="input" type="text" bind:value={a.form.title} />
            </div>
            <div class="control">
              <label class="label is-small" for="ff-match-year">Year</label>
              <input id="ff-match-year" class="input ff-match-year" type="number" bind:value={a.form.year} />
            </div>
            <div class="control">
              <label class="label is-small" for="ff-match-imdb">IMDb ID</label>
              <input id="ff-match-imdb" class="input" type="text" placeholder="tt..." bind:value={a.form.imdbId} />
            </div>
          </div>
          <div class="ff-settings-actions">
            {#if d.guessTitle}
              <button class="button is-small" onclick={() => app.useGuess()}>Use folder guess: {d.guessTitle}{d.guessYear ? ' (' + d.guessYear + ')' : ''}</button>
            {/if}
            <button class="button is-link" class:is-loading={a.searching} onclick={() => app.searchOmdb()}>Search OMDb</button>
          </div>
        </div>

        {#if a.candidates !== null}
          {#if a.candidates.length}
            <div class="ff-candidates">
              {#each a.candidates as c}
                <div class="box ff-candidate">
                  {#if c.hasPoster}
                    <img class="ff-candidate-poster" src={'/api/admin/omdb/poster/' + c.imdbId} alt={c.title} />
                  {:else}
                    <div class="ff-candidate-poster ff-candidate-noposter">no poster</div>
                  {/if}
                  <div class="ff-candidate-body">
                    <p class="has-text-weight-semibold">{c.title} {#if c.year}<span class="has-text-grey">({c.year})</span>{/if}</p>
                    <p class="is-size-7 has-text-grey">{c.type} &middot; {c.imdbId}</p>
                    <button class="button is-small is-primary" class:is-loading={a.applying} onclick={() => app.applyMatch(c)}>Use this match</button>
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
  <!-- List: one row per problem, one button per row. -->
  <div class="ff-page-head">
    <h1 class="title is-4">Needs attention</h1>
    <div class="buttons">
      <button class="button" disabled={app.discoveryRunning} onclick={() => app.runDiscovery()}>
        {app.discoveryRunning ? 'Starting...' : 'Run discovery now'}
      </button>
    </div>
  </div>
  <p class="ff-settings-intro has-text-grey">
    Everything the library cannot fix on its own. Each row names one problem and carries the
    one action that settles it.
  </p>

  <div class="ff-attention-chips">
    {#each chips as c}
      <button
        class="button is-small ff-chip"
        class:is-link={a.filter === c.key}
        onclick={() => app.setAttentionFilter(c.key)}
      >{c.label}<span class="ff-chip-count">{a.counts[c.key] ?? 0}</span></button>
    {/each}
  </div>

  {#if a.loading}
    <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
  {:else if rows.length}
    <table class="table is-fullwidth ff-attention-table">
      <thead>
        <tr><th>Item</th><th>Where</th><th>Problem</th><th>What is wrong</th><th>Fix</th></tr>
      </thead>
      <tbody>
        {#each rows as row (row.key)}
          <tr>
            <td>
              <a href={null} class="ff-edit-link" title="Edit metadata" onclick={() => app.goEditMeta(row.id)}>{row.title}</a>
              {#if row.folder}<span class="ff-attention-folder">{row.folder}</span>{/if}
            </td>
            <td class="has-text-grey">{row.category || '-'}</td>
            <td><span class="tag ff-health-tag ff-problem-{row.problem}">{problemLabel[row.problem]}</span></td>
            <td>
              {#if row.problem === 'name'}
                {#if row.plan.folder.from !== row.plan.folder.to}
                  <span class="has-text-grey">{row.plan.folder.from}</span>
                  <span class="ff-attention-arrow">&rarr;</span>{row.plan.folder.to}
                {:else}
                  the folder name is right, the files inside are not
                {/if}
                {#if row.plan.files.length}
                  <span class="ff-attention-then">{row.plan.files.length} file{row.plan.files.length === 1 ? '' : 's'} renamed with it</span>
                {/if}
              {:else}
                {row.what}
                {#if row.then}<span class="ff-attention-then">{row.then}</span>{/if}
              {/if}
              {#if row.problem === 'disk' && a.openDetail === row.key}
                <ul class="ff-attention-help">
                  {#each row.issues as iss}
                    <li><span class="has-text-weight-semibold">{iss.detail}</span> {issueHelp[iss.code] || ''}</li>
                  {/each}
                </ul>
              {/if}
            </td>
            <td class="ff-attention-fix">
              {#if row.problem === 'metadata'}
                <button class="button is-small is-link" onclick={() => app.goAttention(row.id)}>Find match</button>
              {:else if row.problem === 'name'}
                <button class="button is-small is-link" class:is-loading={a.renaming === row.id} onclick={() => app.renameMedia(row)}>Rename</button>
              {:else if row.problem === 'category'}
                <button class="button is-small" onclick={() => app.go('/media/' + row.id)}>Review</button>
              {:else}
                <button class="button is-small" onclick={() => app.toggleAttentionDetail(row.key)}>
                  {a.openDetail === row.key ? 'Hide' : 'Details'}
                </button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {:else if a.counts.all}
    <p class="help">Nothing of that kind needs attention.</p>
  {:else}
    <p class="help">Nothing needs attention.</p>
  {/if}
{/if}
