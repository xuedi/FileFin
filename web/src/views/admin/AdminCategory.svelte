<script>
  import { getContext } from 'svelte'
  const app = getContext('app')

  const d = $derived(app.catDetail)
  const f = $derived(app.catForm)

  // Why the Delete button is off, in the words of the rule that forbids it.
  const deleteReason = $derived(
    !d ? '' : d.hasSubs ? 'This category still holds sub-categories. Delete those first.'
      : !d.empty ? 'This category still holds media. Move or remove it first.'
      : 'Removes the empty folder and its config file.',
  )
</script>

<button class="button is-ghost is-small ff-back" onclick={() => app.go('/admin/library')}>&larr; Library</button>
{#if app.catError}<p class="has-text-danger">{app.catError}</p>{/if}
{#if !d || !f}
  <p class="has-text-grey has-text-centered ff-loading">Loading...</p>
{:else}
  <h1 class="title is-4">{d.alias}</h1>
  <p class="ff-settings-intro has-text-grey">In <code>{d.name}</code></p>

  <div class="box ff-settings-card">
    <h2 class="title is-6">Identity</h2>
    <div class="field">
      <label class="label is-small" for="ff-cat-folder">Folder</label>
      <input id="ff-cat-folder" class="input" type="text" value={d.leaf} readonly />
      <p class="help">The folder on disk. It is the path the media lives under, so it never changes here.</p>
    </div>
    <div class="field">
      <label class="label is-small" for="ff-cat-alias">Alias</label>
      <input id="ff-cat-alias" class="input" type="text" bind:value={f.alias} />
      <p class="help">The name shown everywhere in the app instead of the folder name.</p>
    </div>
    <div class="field">
      {#if d.topLevel}
        <label class="checkbox">
          <input type="checkbox" bind:checked={f.otherMedia} />
          Other media
        </label>
        <p class="help">
          Home videos and recordings: no metadata is looked up, and posters are taken from a video
          frame. It applies to every sub-category below this one.
        </p>
      {:else}
        <p class="label is-small">Other media</p>
        <p class="help">
          {d.inherited ? 'On, inherited from the top-level category.' : 'Off, inherited from the top-level category.'}
        </p>
      {/if}
    </div>
  </div>

  <div class="box ff-settings-card">
    <h2 class="title is-6">What belongs here</h2>
    <p class="help ff-marker-intro">
      These describe the media this category takes. The import page uses them to preselect a
      category for each row it recognises.
    </p>
    <div class="field">
      <label class="label is-small" for="ff-cat-kind">Kind</label>
      <div class="select">
        <select id="ff-cat-kind" bind:value={f.kind}>
          <option value="both">Films and shows</option>
          <option value="films">Films only</option>
          <option value="shows">Shows only</option>
        </select>
      </div>
      <p class="help">Only media recognised as this kind is ever suggested for this category.</p>
    </div>
    <div class="field">
      <label class="label is-small" for="ff-cat-languages">Languages</label>
      <input id="ff-cat-languages" class="input" type="text" placeholder="Korean, English" bind:value={f.languages} />
      <p class="help">
        Comma separated. A media whose looked-up language is one of these belongs here; a mismatch
        is reported on the Unhealthy media page.
      </p>
    </div>
    <div class="field">
      <label class="label is-small" for="ff-cat-countries">Countries</label>
      <input id="ff-cat-countries" class="input" type="text" placeholder="South Korea" bind:value={f.countries} />
      <p class="help">Comma separated. Used the same way as the languages, against the looked-up country.</p>
    </div>
    <div class="field">
      <label class="label is-small" for="ff-cat-keywords">Keywords</label>
      <input id="ff-cat-keywords" class="input" type="text" placeholder="kdrama, tving" bind:value={f.keywords} />
      <p class="help">Comma separated. A source name containing one of these words is a vote for this category.</p>
    </div>
    <div class="ff-settings-actions">
      <button class="button is-primary" class:is-loading={app.catSaving} onclick={() => app.saveCategory()}>Save</button>
    </div>
  </div>

  <div class="box ff-settings-card">
    <h2 class="title is-6">Learned from imports</h2>
    <p class="help ff-marker-intro">
      Recorded automatically whenever media is imported here: the release group, the bracket tags,
      the platform, the script of its name. A marker only votes once it has been seen twice and
      nearly always lands in one category. Remove one that turned out to point the wrong way.
    </p>
    {#if d.learned.length}
      <table class="table is-fullwidth">
        <thead>
          <tr><th>Marker</th><th>Times</th><th>Also seen in</th><th></th></tr>
        </thead>
        <tbody>
          {#each d.learned as row}
            <tr>
              <td><code>{row.marker}</code></td>
              <td>{row.count}</td>
              <td class="has-text-grey">{row.alsoIn.length ? row.alsoIn.join(', ') : '-'}</td>
              <td class="ff-row-actions">
                <button class="button is-small is-danger is-light" onclick={() => app.removeLearnedMarker(row.marker)}>Remove</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <p class="help">Nothing learned yet. The first import into this category starts it off.</p>
    {/if}
  </div>

  <div class="box ff-settings-card">
    <h2 class="title is-6">What this category holds</h2>
    <table class="table ff-meta-table"><tbody>
      <tr><th>Media</th><td>{d.media}</td></tr>
      <tr><th>Media files</th><td>{d.files}</td></tr>
      <tr><th>Sub-categories</th><td>{d.hasSubs ? 'yes' : 'none'}</td></tr>
    </tbody></table>
  </div>

  <div class="box ff-settings-card">
    <h2 class="title is-6">Delete</h2>
    <p class="help ff-marker-intro">{deleteReason}</p>
    <div class="ff-settings-actions">
      <button class="button is-danger" disabled={!d.empty} onclick={() => app.deleteCategory(d.name)}>Delete category</button>
    </div>
  </div>
{/if}
