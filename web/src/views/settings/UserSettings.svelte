<script>
  import { getContext } from 'svelte'

  const app = getContext('app')

  // Seed the username field from the loaded profile when the page opens.
  app.mdl.username = app.me.mdlUsername || ''
</script>

<h1 class="title is-5">Settings</h1>

<div class="box ff-settings-card">
  <h2 class="title is-6">Account</h2>
  <table class="table ff-sys-table">
    <tbody>
      <tr><td>Username</td><td>{app.me.user}</td></tr>
      {#if app.me.alias}
        <tr><td>Display name</td><td>{app.me.alias}</td></tr>
      {/if}
      <tr><td>Role</td><td>{app.me.admin ? 'Administrator' : 'User'}</td></tr>
    </tbody>
  </table>
</div>

<div class="box ff-settings-card">
  <h2 class="title is-6">MyDramaList</h2>
  <p class="help ff-settings-intro">
    Import what you have watched and your 1-10 ratings from your MyDramaList profile. Your list must
    be public. Titles are matched to this library approximately, so you confirm the matches before
    anything is saved.
  </p>

  <div class="field has-addons">
    <div class="control is-expanded">
      <input class="input" type="text" placeholder="MyDramaList username" bind:value={app.mdl.username} />
    </div>
    <div class="control">
      <button class="button" onclick={() => app.saveMDLUsername()}>Save</button>
    </div>
  </div>

  <div class="field">
    <button
      class="button is-link"
      class:is-loading={app.mdl.loading}
      disabled={!app.me.mdlUsername}
      onclick={() => app.mdlPreview()}>Import from MyDramaList</button>
    {#if !app.me.mdlUsername}
      <p class="help">Save a username first.</p>
    {/if}
  </div>

  {#if app.mdl.preview}
    {@const p = app.mdl.preview}
    <hr />
    <p class="ff-settings-intro">
      Found {p.total} title{p.total === 1 ? '' : 's'}: {p.matched.length} matched, {p.unmatched.length} not in this library.
    </p>

    {#if p.matched.length}
      <table class="table is-fullwidth is-hoverable">
        <thead>
          <tr><th></th><th>Library title</th><th>MyDramaList</th><th>Rating</th><th>Watched</th></tr>
        </thead>
        <tbody>
          {#each p.matched as m}
            <tr>
              <td><input type="checkbox" bind:checked={m.selected} /></td>
              <td>
                {m.libraryTitle}
                {#if !m.exact}<span class="tag is-warning is-light ff-health-tag" title="Matched by title only">approx</span>{/if}
              </td>
              <td class="has-text-grey">{m.mdlTitle}{m.year ? ' (' + m.year + ')' : ''}</td>
              <td>{m.rating ? m.rating + '/10' : '-'}</td>
              <td>{m.willMarkWatched ? '✓' : ''}</td>
            </tr>
          {/each}
        </tbody>
      </table>

      <div class="ff-settings-actions">
        <button class="button" onclick={() => (app.mdl.preview = null)}>Cancel</button>
        <button class="button is-link" class:is-loading={app.mdl.applying} onclick={() => app.mdlApply()}>
          Confirm import
        </button>
      </div>
    {:else}
      <p class="help">No matches against this library.</p>
    {/if}

    {#if p.unmatched.length}
      <details class="ff-advanced">
        <summary>{p.unmatched.length} unmatched title{p.unmatched.length === 1 ? '' : 's'}</summary>
        <ul class="ff-cast">{#each p.unmatched as t}<li>{t}</li>{/each}</ul>
      </details>
    {/if}
  {/if}
</div>
