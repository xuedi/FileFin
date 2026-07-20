<script>
  import { getContext } from 'svelte'
  import { treeMarker } from '../../lib/app.svelte.js'
  const app = getContext('app')

  let overName = $state('') // row currently a valid drop target, for the drop-line cue

  // A drop is allowed only onto a different category at the same level (same parent): order
  // is per sibling group, so calling preventDefault gates the cursor and the drop to siblings.
  function onDragOver(e, c) {
    const d = app.categories.find((x) => x.name === app.dragName)
    if (!d || d.name === c.name || (d.parentId || 0) !== (c.parentId || 0)) return
    e.preventDefault()
    overName = c.name
  }
  function onDrop(e, c) {
    e.preventDefault()
    overName = ''
    app.reorderCategory(c)
  }

  const kindLabel = { films: 'films only', shows: 'shows only', both: 'films and shows' }
</script>

<h1 class="title is-4">Library</h1>
<p class="ff-settings-intro has-text-grey">
  Every category, in the order they are shown. Drag a row to reorder it within its level; open one
  to change its alias and what belongs in it.
</p>
{#if app.catError}<p class="has-text-danger">{app.catError}</p>{/if}
<table class="table is-fullwidth">
  <thead>
    <tr>
      <th>Folder</th>
      <th>Alias</th>
      <th title="Media items in this category (each a movie or one TV show), with total media files in parentheses.">Media</th>
      <th title="Other media (home videos / recordings): skips OMDb lookups and derives posters from a video frame instead.">Other media</th>
      <th title="What belongs here: the kind of media the category takes, and how much past imports have taught it.">Markers</th>
      <th></th>
      <th></th>
    </tr>
  </thead>
  <tbody>
    {#each app.categoryTree as c}
      <tr
        class:ff-drag-over={overName === c.name}
        ondragover={(e) => onDragOver(e, c)}
        ondragleave={() => overName === c.name && (overName = '')}
        ondrop={(e) => onDrop(e, c)}>
        <td><span class="ff-cat-tree">{treeMarker(c._depth)}</span>{c.leaf ?? c.name}</td>
        <td>{c.alias}</td>
        <td>{c.media ?? 0} ({c.files ?? 0})</td>
        <td class="has-text-centered">
          {#if c.otherMedia}
            <span class="tag is-light">other media</span>
          {:else if c._depth > 0}
            <span class="has-text-grey is-size-7 is-italic" title="Inherited from the top-level category">inherited</span>
          {/if}
        </td>
        <td class="is-size-7 has-text-grey">
          {kindLabel[c.kind] ?? kindLabel.both}{#if c.learned > 0} &middot; {c.learned} learned{/if}
        </td>
        <td class="ff-row-actions">
          <button class="button is-small" onclick={() => app.openCategory(c)}>Edit</button>
        </td>
        <td
          class="ff-drag-handle"
          draggable="true"
          ondragstart={() => (app.dragName = c.name)}
          ondragend={() => ((app.dragName = ''), (overName = ''))}
          title="Drag to reorder within this level">⠿</td>
      </tr>
    {/each}
    {#if app.categories.length === 0}
      <tr><td colspan="7" class="has-text-grey">No categories yet.</td></tr>
    {/if}
  </tbody>
</table>
<div class="ff-add-row">
  <input class="input" placeholder="Folder name" bind:value={app.catName} />
  <input class="input" placeholder="Alias (defaults to folder name)" bind:value={app.catAlias} />
  <div class="select">
    <select bind:value={app.catParentId}>
      <option value={0}>(top level)</option>
      {#each app.categoryTree as c}
        <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
      {/each}
    </select>
  </div>
  <button class="button is-primary" disabled={!app.catName.trim()} onclick={() => app.addCategory()}>Add</button>
</div>
