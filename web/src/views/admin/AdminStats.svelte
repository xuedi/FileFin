<script>
  import { getContext } from 'svelte'
  import ProgressBar from '../../components/ProgressBar.svelte'

  const app = getContext('app')

  // Chart.js is lazy-loaded (a separate bundle chunk, like hls.js) so it never weighs on the
  // rest of the app; the page renders its tables immediately and the canvases fill in once the
  // library is parsed.
  let Chart = $state(null)
  const canvases = {} // name -> <canvas>, bound below
  let charts = [] // live Chart instances, destroyed on rebuild/teardown

  // The accent and a small categorical palette for the bar charts, plus semantic colours for
  // the playability doughnut so the buckets read consistently.
  const accent = '#4f8cff'
  const palette = ['#4f8cff', '#48c78e', '#ffe08a', '#f14668', '#a78bfa', '#3e8ed0', '#ff9f43', '#7a7a7a']
  const playColors = {
    'Direct play': '#48c78e',
    Remux: '#3e8ed0',
    Optimized: '#4f8cff',
    'Needs optimize': '#f14668',
    Unprobed: '#7a7a7a',
  }

  $effect(() => {
    let cancelled = false
    import('chart.js/auto').then((m) => {
      if (cancelled) return
      m.default.defaults.color = '#b5b5b5'
      m.default.defaults.borderColor = 'rgba(255,255,255,0.08)'
      m.default.defaults.font.family = 'inherit'
      Chart = m.default
    })
    return () => {
      cancelled = true
    }
  })

  // Rebuild every chart whenever the data or the library is ready. The effect's cleanup
  // destroys the instances, so leaving the page (or a data refresh) never leaks a canvas.
  $effect(() => {
    const s = app.stats
    if (!Chart || !s) return
    charts.forEach((c) => c.destroy())
    charts = []
    bar('containers', s.containers)
    bar('video', s.videoCodecs)
    bar('audio', s.audioCodecs)
    bar('tags', s.tags)
    doughnut('play', s.playability)
    return () => {
      charts.forEach((c) => c.destroy())
      charts = []
    }
  })

  function bar(name, rows) {
    const el = canvases[name]
    if (!el || !rows?.length) return
    charts.push(
      new Chart(el, {
        type: 'bar',
        data: {
          labels: rows.map((r) => r.label),
          datasets: [{ data: rows.map((r) => r.count), backgroundColor: accent, borderRadius: 3 }],
        },
        options: {
          indexAxis: 'y',
          responsive: true,
          maintainAspectRatio: false,
          plugins: { legend: { display: false } },
          scales: { x: { beginAtZero: true, ticks: { precision: 0 } } },
        },
      }),
    )
  }

  function doughnut(name, rows) {
    const el = canvases[name]
    if (!el || !rows?.length) return
    charts.push(
      new Chart(el, {
        type: 'doughnut',
        data: {
          labels: rows.map((r) => r.label),
          datasets: [{ data: rows.map((r) => r.count), backgroundColor: rows.map((r, i) => playColors[r.label] || palette[i % palette.length]) }],
        },
        options: { responsive: true, maintainAspectRatio: false, plugins: { legend: { position: 'right' } } },
      }),
    )
  }
</script>

<div class="ff-page-head">
  <h1 class="title is-4">Statistics</h1>
</div>

{#if app.stats}
  {@const s = app.stats}
  <div class="ff-dash">
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.coverage.totalMedia}</span>
      <span class="ff-dash-label">Media items</span>
    </div>
    <div class="box ff-dash-card">
      <span class="ff-dash-num">{s.coverage.totalFiles}</span>
      <span class="ff-dash-label">Media files</span>
    </div>
    <div class="box ff-dash-card ff-stats-coverage">
      <span class="ff-dash-label">Optimize coverage - {s.coverage.optimized}/{s.coverage.optimized + s.coverage.pending} files that need a copy</span>
      <ProgressBar value={s.coverage.optimized} max={s.coverage.optimized + s.coverage.pending || 1} />
    </div>
  </div>

  <div class="columns is-multiline ff-stats-grid">
    <div class="column is-half">
      <div class="box">
        <h2 class="title is-6">Playability</h2>
        <div class="ff-chart"><canvas bind:this={canvases.play}></canvas></div>
        {@render counts(s.playability)}
      </div>
    </div>
    <div class="column is-half">
      <div class="box">
        <h2 class="title is-6">Containers</h2>
        <div class="ff-chart"><canvas bind:this={canvases.containers}></canvas></div>
        {@render counts(s.containers)}
      </div>
    </div>
    <div class="column is-half">
      <div class="box">
        <h2 class="title is-6">Video codecs</h2>
        <div class="ff-chart"><canvas bind:this={canvases.video}></canvas></div>
        {@render counts(s.videoCodecs)}
      </div>
    </div>
    <div class="column is-half">
      <div class="box">
        <h2 class="title is-6">Audio codecs</h2>
        <div class="ff-chart"><canvas bind:this={canvases.audio}></canvas></div>
        {@render counts(s.audioCodecs)}
      </div>
    </div>
    {#if s.tags?.length}
      <div class="column is-half">
        <div class="box">
          <h2 class="title is-6">Tags</h2>
          <div class="ff-chart"><canvas bind:this={canvases.tags}></canvas></div>
          {@render counts(s.tags)}
        </div>
      </div>
    {/if}
  </div>
{:else}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{/if}

{#snippet counts(rows)}
  <table class="table is-fullwidth is-narrow ff-stats-table">
    <tbody>
      {#each rows as r}
        <tr><td>{r.label}</td><td class="has-text-right has-text-grey">{r.count}</td></tr>
      {/each}
    </tbody>
  </table>
{/snippet}
