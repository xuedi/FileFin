<script>
  import { getContext } from 'svelte'
  import { episodeLabel, humanSize, preferredSubtitle } from '../../lib/app.svelte.js'
  const app = getContext('app')

  let detail = $derived(app.detail)
  let file = $derived(app.currentFileInfo)
  let multi = $derived(detail.files.length > 1)
  let chosen = $derived(file ? preferredSubtitle(file.subtitles, detail.subtitle || '') : -1)

  // How many of the item's files carry each subtitle language, so a series shows at a glance
  // which languages cover every episode and which have gaps.
  let coverage = $derived.by(() => {
    const counts = new Map()
    for (const f of detail.files) {
      for (const lang of new Set(f.subtitles.map((s) => s.lang || 'und'))) {
        const label = f.subtitles.find((s) => (s.lang || 'und') === lang).label || lang
        const c = counts.get(lang) ?? { lang, label, n: 0 }
        c.n++
        counts.set(lang, c)
      }
    }
    return [...counts.values()].sort((a, b) => b.n - a.n || a.label.localeCompare(b.label))
  })

  // The external scores in their own scale: "8.1/10" leads with 8.1, "92%" stays whole.
  function score(v) {
    const m = /^([\d.]+)(\/\d+|%)?/.exec(v)
    return m ? { big: m[1], small: m[2] ?? '' } : { big: v, small: '' }
  }

  // The first rows of a long series cast; the rest wait behind "Show all".
  const castPreview = 12
  let showAllCast = $state(false)
  $effect(() => {
    detail.id
    showAllCast = false
  })
  let shownPeople = $derived(showAllCast ? detail.people : detail.people.slice(0, castPreview))

  function initials(name) {
    const parts = name.split(/\s+/).filter(Boolean)
    return ((parts[0]?.[0] ?? '') + (parts.length > 1 ? parts[parts.length - 1][0] : '')).toUpperCase()
  }

  function pivot(key) {
    return { 'Directed by': 'director', Language: 'language' }[key]
  }
</script>

<section class="ff-info">
  <h3 class="ff-info-heading">Details</h3>
  <div class="ff-info-grid">
    <div class="ff-info-col ff-info-col-main">
      {#if detail.metadata.length || detail.ratings.length || detail.plot}
        <div class="box ff-info-card">
          <h4 class="ff-card-title">About</h4>
          {#if detail.ratings.length}
            <div class="ff-scores">
              {#each detail.ratings as r}
                {@const s = score(r.value)}
                <div class="ff-score">
                  <span class="ff-score-value">{s.big}<small>{s.small}</small></span>
                  <span class="ff-score-label">{r.key}</span>
                </div>
              {/each}
            </div>
          {/if}
          {#if detail.metadata.length}
            <dl class="ff-facts">
              {#each detail.metadata as m}
                <dt>{m.key}</dt>
                <dd>
                  {#if pivot(m.key)}
                    <a
                      href={null}
                      class="ff-pivot"
                      onclick={() => app.go('/search?field=' + pivot(m.key) + '&q=' + encodeURIComponent(m.value))}>{m.value}</a>
                  {:else}
                    {m.value}
                  {/if}
                </dd>
              {/each}
            </dl>
          {/if}
          {#if detail.plot}<p class="ff-plot">{detail.plot}</p>{/if}
        </div>
      {/if}

      {#if detail.people.length}
        <div class="box ff-info-card">
          <h4 class="ff-card-title">Cast</h4>
          <div class="ff-cast-grid">
            {#each shownPeople as p (p.id || p.name)}
              <a href={null} class="ff-person" title={p.character ? p.name + ' as ' + p.character : p.name} onclick={() => app.go('/search?field=cast&q=' + encodeURIComponent(p.name))}>
                {#if p.photo}
                  <img class="ff-person-photo" src={p.photo} alt={p.name} loading="lazy" />
                {:else}
                  <span class="ff-person-photo ff-person-initials">{initials(p.name)}</span>
                {/if}
                <span class="ff-person-name">{p.name}</span>
                {#if p.character}<span class="ff-person-role">{p.character}</span>{/if}
              </a>
            {/each}
          </div>
          {#if detail.people.length > castPreview || detail.castFromTMDb}
            <div class="ff-cast-foot">
              {#if detail.people.length > castPreview}
                <button class="button is-small is-ghost" onclick={() => (showAllCast = !showAllCast)}>
                  {showAllCast ? 'Show fewer' : 'Show all ' + detail.people.length}
                </button>
              {/if}
              {#if detail.castFromTMDb}<span class="ff-cast-credit">Cast data: TMDb</span>{/if}
            </div>
          {/if}
        </div>
      {/if}
    </div>
    <div class="ff-info-col">
      {#if file || detail.technical.length}
        <div class="box ff-info-card">
          <h4 class="ff-card-title">
            File
            {#if multi && file}<span class="ff-card-sub">{episodeLabel(file)}</span>{/if}
            {#if file}
              <span class="tag {file.transcode ? 'is-warning' : 'is-success'} is-light ff-card-badge">
                {file.transcode ? 'Transcoded (HLS)' : 'Direct play'}
              </span>
            {/if}
          </h4>
          <dl class="ff-facts">
            {#each detail.technical as m}<dt>{m.key}</dt><dd>{m.value}</dd>{/each}
            {#if file}
              <dt>Size</dt><dd>{humanSize(file.size)}</dd>
              <dt>Path</dt><dd class="ff-path">{file.path}</dd>
            {/if}
            {#if multi}<dt>Whole item</dt><dd>{humanSize(app.detailBytes)} in {detail.files.length} files</dd>{/if}
          </dl>
        </div>
      {/if}

      {#if file}
        <div class="box ff-info-card">
          <h4 class="ff-card-title">
            Subtitles
            {#if multi}<span class="ff-card-sub">{episodeLabel(file)}</span>{/if}
          </h4>
          {#if file.subtitles.length}
            <ul class="ff-subs">
              {#each file.subtitles as s, i}
                <li class="ff-sub">
                  <div class="ff-sub-head">
                    <strong>{s.label || 'Unlabelled'}</strong>
                    {#if s.lang}<span class="tag is-dark">{s.lang}</span>{/if}
                    {#each s.flags ?? [] as fl}<span class="tag is-info is-light">{fl.toUpperCase()}</span>{/each}
                    {#if s.format}<span class="tag" title={s.format === 'ASS' ? 'ASS/SSA script, shown as plain text' : 'SubRip'}>{s.format}</span>{/if}
                    {#if i === chosen}<span class="tag is-link is-light" title="Turned on automatically when you play this item">your choice</span>{/if}
                  </div>
                  <div class="ff-sub-file" title={s.file}>{s.file} &middot; {humanSize(s.size)}</div>
                </li>
              {/each}
            </ul>
          {:else}
            <p class="has-text-grey">No subtitles for this file.</p>
          {/if}
          {#if multi && coverage.length}
            <div class="ff-coverage">
              {#each coverage as c}
                <div class="ff-coverage-row">
                  <span>{c.label}</span>
                  <progress class="progress is-small {c.n === detail.files.length ? 'is-success' : 'is-warning'}" value={c.n} max={detail.files.length}></progress>
                  <span class="has-text-grey">{c.n}/{detail.files.length}</span>
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/if}

      {#if app.me?.admin}
        <div class="box ff-info-card ff-admin-card">
          <h4 class="ff-card-title">Admin</h4>
          <div class="ff-admin-action">
            <button class="button" onclick={() => app.goEditMeta(detail.id)}>Edit metadata</button>
            <p class="help">Hand-edit every metadata field and replace the poster.</p>
          </div>
          <div class="ff-admin-action">
            <button class="button" disabled={app.repairingSubs} onclick={() => app.repairSubtitles(detail.id)}>
              {app.repairingSubs ? 'Rebuilding...' : 'Rebuild subtitles'}
            </button>
            <p class="help">Extract subtitle tracks embedded in the video files into playable sidecars.</p>
          </div>
        </div>
      {/if}
    </div>
  </div>
</section>
