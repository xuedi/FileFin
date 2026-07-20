<script>
  import { getContext } from 'svelte'
  const app = getContext('app')

  let confirming = $state('') // the tag whose delete is awaiting confirmation

  function onRenameKey(e) {
    if (e.key === 'Enter') app.renameTag()
    else if (e.key === 'Escape') app.cancelTagEdit()
  }
</script>

<h1 class="title is-4">Tags</h1>
<p class="ff-settings-intro has-text-grey">
  Your own classification, alongside the genres the metadata source supplies. Renaming a tag onto
  one that already exists merges the two. Both operations rewrite every item that carries the tag.
</p>

{#if app.adminTags.length}
  <table class="table is-fullwidth">
    <thead>
      <tr>
        <th>Tag</th>
        <th title="Media items carrying this tag.">Items</th>
        <th></th>
      </tr>
    </thead>
    <tbody>
      {#each app.adminTags as t}
        <tr>
          <td>
            {#if app.tagEdit === t.tag}
              <input
                class="input is-small ff-tag-rename"
                bind:value={app.tagEditValue}
                onkeydown={onRenameKey}
                aria-label={'New name for ' + t.tag} />
            {:else}
              <a href={null} onclick={() => app.goTag(t.tag)}>{t.tag}</a>
            {/if}
          </td>
          <td>{t.count}</td>
          <td class="ff-tag-actions">
            {#if app.tagEdit === t.tag}
              <button class="button is-small is-primary" class:is-loading={app.tagBusy} onclick={() => app.renameTag()}>Save</button>
              <button class="button is-small is-ghost" onclick={() => app.cancelTagEdit()}>Cancel</button>
            {:else if confirming === t.tag}
              <span class="has-text-grey">Remove from {t.count} item{t.count === 1 ? '' : 's'}?</span>
              <button class="button is-small is-danger" class:is-loading={app.tagBusy} onclick={() => { confirming = ''; app.deleteTag(t.tag) }}>Delete</button>
              <button class="button is-small is-ghost" onclick={() => (confirming = '')}>Cancel</button>
            {:else}
              <button class="button is-small" onclick={() => app.startTagEdit(t.tag)}>Rename</button>
              <button class="button is-small is-danger is-light" onclick={() => (confirming = t.tag)}>Delete</button>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{:else}
  <p class="has-text-grey">
    No tags yet. Open any item in the library and use <strong>+ tag</strong> to classify it.
  </p>
{/if}
