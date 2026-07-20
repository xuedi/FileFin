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
</script>

<h1 class="title is-4">Library</h1>
{#if app.catError}<p class="has-text-danger">{app.catError}</p>{/if}
<table class="table is-fullwidth">
  <thead>
    <tr>
      <th>Folder</th>
      <th>Alias</th>
      <th title="Media items in this category (each a movie or one TV show), with total media files in parentheses.">Media</th>
      <th title="Other media (home videos / recordings): skips OMDb lookups and derives posters from a video frame instead.">Other media</th>
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
        <td>
          {#if app.editName === c.name}
            <input class="input is-small ff-inline-input" bind:value={app.editAlias} onkeydown={(e) => e.key === 'Enter' && app.saveAlias()} />
          {:else}
            {c.alias}
          {/if}
        </td>
        <td>{c.media ?? 0} ({c.files ?? 0})</td>
        <td class="has-text-centered">
          {#if c._depth === 0}
            <input type="checkbox" checked={c.otherMedia} onchange={(e) => app.toggleOtherMedia(c, e.currentTarget.checked)} />
          {:else}
            <span class="has-text-grey is-size-7 is-italic" title="Inherited from the top-level category">inherited</span>
          {/if}
        </td>
        <td class="ff-row-actions">
          {#if app.editName === c.name}
            <button class="button is-small is-primary" onclick={() => app.saveAlias()}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editName = '')}>Cancel</button>
          {:else}
            <button class="button is-small" onclick={() => app.startEditAlias(c)}>Edit</button>
            <button
              class="button is-small is-danger"
              disabled={!c.empty}
              title={c.empty ? 'Delete this empty category' : 'Folder is not empty'}
              onclick={() => app.deleteCategory(c.name)}>Delete</button>
          {/if}
        </td>
        <td
          class="ff-drag-handle"
          draggable={app.editName !== c.name}
          ondragstart={() => (app.dragName = c.name)}
          ondragend={() => ((app.dragName = ''), (overName = ''))}
          title="Drag to reorder within this level">⠿</td>
      </tr>
    {/each}
    {#if app.categories.length === 0}
      <tr><td colspan="6" class="has-text-grey">No categories yet.</td></tr>
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
