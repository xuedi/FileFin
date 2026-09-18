<script>
  import { getContext } from 'svelte'
  import { mergePreview } from '../../lib/app.svelte.js'
  const app = getContext('app')
  const m = $derived(app.merge)
  const v = $derived(m.view)

  // Rows that need a decision come first, then the ones worth a look (sources that differ, a
  // value neither source has, an earlier pick or rule), and the settled ones fold away.
  const decide = $derived(v ? v.rows.filter((r) => r.status === 'conflict') : [])
  const open = $derived(v ? v.rows.filter((r) => ['differ', 'local', 'chosen', 'manual', 'rule'].includes(r.status)) : [])
  const settled = $derived(v ? v.rows.filter((r) => ['agree', 'single', 'empty'].includes(r.status)) : [])
  const identity = $derived(decide.some((r) => r.class === 'identity'))
  const usable = $derived(v ? v.sources.filter((s) => !s.error) : [])
  const rules = $derived(Object.keys(m.remember).filter((f) => m.remember[f] && usable.some((s) => s.name === m.picks[f])))
  let takeAllRemember = $state(false)

  const statusText = {
    conflict: 'conflict',
    differ: 'sources differ',
    local: 'neither source',
    chosen: 'picked',
    manual: 'kept',
    rule: 'by rule',
    agree: 'agree',
    single: 'one source',
    empty: 'no value',
  }

  function show(vals) {
    return vals && vals.length ? vals.join(', ') : '-'
  }

  function labelOf(field) {
    return v.rows.find((r) => r.field === field)?.label ?? field
  }
</script>

<button class="button is-ghost is-small ff-back" onclick={() => app.go('/admin/attention?problem=conflict')}>&larr; Needs attention</button>

{#if !v}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{:else}
  <div class="ff-page-head">
    <div>
      <h1 class="title is-4">{v.title} {#if v.year}<span class="has-text-grey">({v.year})</span>{/if}</h1>
      <p class="ff-settings-intro has-text-grey">{v.folder}{#if v.category} &middot; in {v.category}{/if}</p>
    </div>
    <div class="buttons">
      <button class="button" onclick={() => app.goEditMeta(v.id)}>Edit metadata</button>
    </div>
  </div>

  <div class="ff-merge-sources">
    {#each v.sources as s (s.name)}
      <div class="box ff-merge-source">
        {#if s.poster}<img src={s.poster} alt={s.label + ' poster'} />{/if}
        <div>
          <p class="has-text-weight-semibold">{s.label}</p>
          {#if s.error}
            <p class="is-size-7 has-text-grey">{s.error} &middot; tried {s.fetched}</p>
          {:else}
            <p class="is-size-7 has-text-grey">{s.kind} &middot; {s.id}{#if s.imdbId && s.imdbId !== s.id} &middot; {s.imdbId}{/if}</p>
            <p class="is-size-7 has-text-grey">fetched {s.fetched}</p>
          {/if}
          <button class="button is-small ff-merge-rematch" onclick={() => app.goAttention(v.id, s.name)}>Re-match {s.label}</button>
        </div>
      </div>
    {/each}
  </div>

  {#if identity}
    <div class="notification is-danger is-light">
      The sources disagree on {decide.filter((r) => r.class === 'identity').map((r) => r.label.toLowerCase()).join(' and ')}:
      they probably describe different works. Re-match the wrong one before merging fields.
    </div>
  {/if}

  {#if usable.length > 1}
    <div class="ff-merge-toolbar">
      {#each usable as s (s.name)}
        <button class="button is-small" onclick={() => app.takeAllFrom(s.name, takeAllRemember)}>Take all {s.label}</button>
      {/each}
      <label class="checkbox is-size-7"><input type="checkbox" bind:checked={takeAllRemember} /> remember as rules for all items</label>
      <span class="ff-merge-count">{decide.length} conflict{decide.length === 1 ? '' : 's'}</span>
    </div>
  {/if}

  {#snippet fieldRow(r)}
    {@const pick = m.picks[r.field]}
    <tr class:ff-merge-conflict={r.status === 'conflict'}>
      <th>
        {r.label}
        <span class="tag is-small ff-merge-status ff-merge-status-{r.status}">{statusText[r.status] ?? r.status}</span>
        {#if r.rule}<span class="ff-merge-rule">rule: {r.rule.join(' > ')}</span>{/if}
      </th>
      {#each v.sources as s (s.name)}
        <td>
          {#if r.values[s.name]}
            {#if r.pickable}
              <label class="radio ff-merge-pick">
                <input type="radio" name={'pick-' + r.field} checked={pick === s.name} onchange={() => app.setMergePick(r.field, s.name)} />
                <span class:ff-merge-prose={r.field === 'description'}>{show(r.values[s.name])}</span>
              </label>
            {:else}
              <span>{show(r.values[s.name])}</span>
            {/if}
          {:else}
            <span class="has-text-grey">-</span>
          {/if}
        </td>
      {/each}
      <td>
        {#if r.pickable && Object.keys(r.values).length}
          {#if r.list && Object.keys(r.values).length > 1}
            <label class="radio ff-merge-pick">
              <input type="radio" name={'pick-' + r.field} checked={pick === 'union'} onchange={() => app.setMergePick(r.field, 'union')} />
              <span>both</span>
            </label>
          {/if}
          <label class="radio ff-merge-pick">
            <input type="radio" name={'pick-' + r.field} checked={pick === 'manual'} onchange={() => app.setMergePick(r.field, 'manual')} />
            <span class:ff-merge-prose={r.field === 'description'}>keep: {show(r.current)}</span>
          </label>
          {#if pick && mergePreview(r, pick).join('|') !== r.current.join('|')}
            <span class="ff-merge-becomes">becomes: {show(mergePreview(r, pick))}</span>
          {/if}
        {:else}
          <span class:ff-merge-prose={r.field === 'description'}>{show(r.current)}</span>
        {/if}
      </td>
      <td class="ff-merge-remember">
        {#if r.pickable && usable.some((s) => s.name === pick)}
          <label class="checkbox is-size-7" title="Use this source for this field on every item">
            <input type="checkbox" checked={!!m.remember[r.field]} onchange={() => app.toggleMergeRemember(r.field)} /> rule
          </label>
        {/if}
      </td>
    </tr>
  {/snippet}

  <table class="table is-fullwidth ff-merge-table">
    <thead>
      <tr>
        <th>Field</th>
        {#each v.sources as s (s.name)}<th>{s.label}</th>{/each}
        <th>This item</th>
        <th></th>
      </tr>
    </thead>
    <tbody>
      {#each decide as r (r.field)}{@render fieldRow(r)}{/each}
      {#each open as r (r.field)}{@render fieldRow(r)}{/each}
      {#if m.showSettled}
        {#each settled as r (r.field)}{@render fieldRow(r)}{/each}
      {/if}
      {#if usable.some((s) => s.poster)}
        <tr>
          <th>Poster</th>
          {#each v.sources as s (s.name)}
            <td>
              {#if s.poster && !s.error}
                <label class="radio ff-merge-pick">
                  <input type="radio" name="pick-poster" checked={m.poster === s.name} onchange={() => (m.poster = s.name)} />
                  <img class="ff-merge-poster" src={s.poster} alt={s.label + ' poster'} />
                </label>
              {/if}
            </td>
          {/each}
          <td>
            <label class="radio ff-merge-pick">
              <input type="radio" name="pick-poster" checked={m.poster === ''} onchange={() => (m.poster = '')} />
              {#if v.hasPoster}
                <img class="ff-merge-poster" src={'/api/media/' + v.id + '/poster?size=tile'} alt="current poster" />
              {:else}
                <span>keep (none)</span>
              {/if}
            </label>
          </td>
          <td></td>
        </tr>
      {/if}
    </tbody>
  </table>
  {#if settled.length}
    <button class="button is-small is-ghost" onclick={() => (m.showSettled = !m.showSettled)}>
      {m.showSettled ? 'Hide' : 'Show'} {settled.length} field{settled.length === 1 ? '' : 's'} the sources agree on
    </button>
  {/if}

  <div class="ff-merge-footer">
    <span class="has-text-grey">
      {app.mergeChanges} field{app.mergeChanges === 1 ? '' : 's'} will change{#if rules.length}; new rules: {rules.map(labelOf).join(', ')}{/if}
    </span>
    <div class="buttons">
      <button class="button" onclick={() => app.go('/admin/attention?problem=conflict')}>Cancel</button>
      <button class="button is-primary" class:is-loading={m.applying} disabled={!app.mergeDirty} onclick={() => app.applyMerge()}>Apply</button>
    </div>
  </div>
{/if}
