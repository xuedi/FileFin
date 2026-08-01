<script>
  import { humanSize } from '../lib/app.svelte.js'

  // The panel behind a Dup marker: what the import would bring in, beside what the library
  // already holds, so the admin can tell which copy is the better one. It is a pure view -
  // loading and caching live on AppState, because both import tables share them.
  let { data = null, loading = false, error = '' } = $props()

  const clock = (sec) => {
    if (!sec) return ''
    const h = Math.floor(sec / 3600)
    const m = Math.floor((sec % 3600) / 60)
    const s = sec % 60
    const pad = (n) => String(n).padStart(2, '0')
    return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
  }

  const res = (side) => (side.width && side.height ? `${side.width}x${side.height}` : '')
  const dash = (v) => (v === '' || v === undefined || v === null ? '-' : v)

  // A fact is only marked when both sides state it and they differ, so the marker means
  // "this one is bigger", never "the other side is unknown".
  const better = (a, b) => (!a || !b || a === b ? '' : a > b ? 'incoming' : 'library')

  const rows = $derived.by(() => {
    if (!data) return []
    const inc = data.incoming
    const lib = data.library
    const files = (side) => (side.files > 1 ? `${side.files} (${humanSize(side.bytes)} in total)` : side.files)
    return [
      { label: 'File', incoming: inc.path, library: lib.path, wrap: true },
      { label: 'Category', incoming: '', library: lib.category },
      { label: 'Files', incoming: files(inc), library: files(lib) },
      {
        label: 'Size',
        incoming: humanSize(inc.fileBytes),
        library: humanSize(lib.fileBytes),
        mark: better(inc.fileBytes, lib.fileBytes),
      },
      {
        label: 'Resolution',
        incoming: res(inc),
        library: res(lib),
        mark: better(inc.width * inc.height, lib.width * lib.height),
      },
      { label: 'Video', incoming: inc.videoCodec, library: lib.videoCodec },
      { label: 'Audio', incoming: inc.audioCodec, library: lib.audioCodec },
      { label: 'Container', incoming: inc.container, library: lib.container },
      { label: 'Duration', incoming: clock(inc.duration), library: clock(lib.duration) },
      { label: 'Modified', incoming: inc.modified, library: lib.modified },
    ]
  })
</script>

<div class="ff-dup-panel">
  {#if loading}
    <p class="has-text-grey">Reading both copies...</p>
  {:else if error}
    <p class="has-text-danger">{error}</p>
  {:else if data}
    <table class="table is-narrow ff-dup-table">
      <thead>
        <tr><th></th><th>Incoming</th><th>In library</th></tr>
      </thead>
      <tbody>
        {#each rows as r}
          <tr>
            <th>{r.label}</th>
            <td class:ff-dup-wrap={r.wrap} class:ff-dup-better={r.mark === 'incoming'}>{dash(r.incoming)}</td>
            <td class:ff-dup-wrap={r.wrap} class:ff-dup-better={r.mark === 'library'}>{dash(r.library)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
