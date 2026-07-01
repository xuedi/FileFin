<script>
  import { getContext } from 'svelte'
  import { treeMarker } from '../../lib/app.svelte.js'

  const app = getContext('app')

  // Seed the username fields from the loaded profile and load the categories the scope
  // dropdowns offer (the import flow is auth-gated, so a plain user can read them too).
  app.mdl.username = app.me.mdlUsername || ''
  app.mal.username = app.me.malUsername || ''
  app.loadScopeCategories()
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

{#snippet scope(store, id)}
  <div class="field">
    <label class="label" for={id}>Limit to category</label>
    <div class="control">
      <div class="select">
        <select {id} bind:value={store.categoryId}>
          <option value={0}>All categories</option>
          {#each app.categoryTree as c}
            <option value={c.id}>{treeMarker(c._depth)}{c.alias}</option>
          {/each}
        </select>
      </div>
    </div>
    <p class="help">Match only against this category and its sub-categories.</p>
  </div>
{/snippet}

{#snippet previewTable(store, sourceTitle, label)}
  {@const p = store.preview}
  <hr />
  <p class="ff-settings-intro">
    Found {p.total} title{p.total === 1 ? '' : 's'}: {p.matched.length} matched, {p.unmatched.length} not in this library.
  </p>

  {#if p.matched.length}
    <table class="table is-fullwidth is-hoverable">
      <thead>
        <tr><th></th><th>Library title</th><th>{label}</th><th>Rating</th><th>Watched</th></tr>
      </thead>
      <tbody>
        {#each p.matched as m}
          <tr>
            <td><input type="checkbox" bind:checked={m.selected} /></td>
            <td>
              {m.libraryTitle}
              {#if m.confidence === 'exact'}
                <span class="tag is-success is-light ff-health-tag" title="Matched on title and year">exact</span>
              {:else if m.confidence === 'confident'}
                <span class="tag is-info is-light ff-health-tag" title="Unique title; year absent or close">confident</span>
              {:else}
                <span class="tag is-warning is-light ff-health-tag" title="Matched approximately">
                  {m.reason
                    ? 'approx: ' + m.reason
                    : m.year && m.libraryYear && m.year !== m.libraryYear
                      ? 'approx: year ' + m.year + ' != ' + m.libraryYear
                      : 'approx'}
                </span>
              {/if}
            </td>
            <td class="has-text-grey">{sourceTitle(m)}{m.year ? ' (' + m.year + ')' : ''}</td>
            <td>{m.rating ? m.rating + '/10' : '-'}</td>
            <td>{m.willMarkWatched ? '✓' : ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>

    <div class="ff-settings-actions">
      <button class="button" onclick={() => (store.preview = null)}>Cancel</button>
      <button class="button is-link" class:is-loading={store.applying} onclick={() => (store === app.mdl ? app.mdlApply() : app.malApply())}>
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
{/snippet}

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

  {@render scope(app.mdl, 'ff-mdl-scope')}

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
    {@render previewTable(app.mdl, (m) => m.mdlTitle, 'MyDramaList')}
  {/if}
</div>

<div class="box ff-settings-card">
  <h2 class="title is-6">MyAnimeList</h2>
  <p class="help ff-settings-intro">
    Import what you have watched and your 1-10 ratings from your MyAnimeList profile. Your list must
    be public. Titles are matched to this library approximately, so you confirm the matches before
    anything is saved.
  </p>

  <div class="field has-addons">
    <div class="control is-expanded">
      <input class="input" type="text" placeholder="MyAnimeList username" bind:value={app.mal.username} />
    </div>
    <div class="control">
      <button class="button" onclick={() => app.saveMALUsername()}>Save</button>
    </div>
  </div>

  {@render scope(app.mal, 'ff-mal-scope')}

  <div class="field">
    <button
      class="button is-link"
      class:is-loading={app.mal.loading}
      disabled={!app.me.malUsername || !app.me.malConfigured}
      onclick={() => app.malPreview()}>Import from MyAnimeList</button>
    {#if !app.me.malConfigured}
      <p class="help">MyAnimeList import is not configured on this server yet.</p>
    {:else if !app.me.malUsername}
      <p class="help">Save a username first.</p>
    {/if}
  </div>

  {#if app.mal.preview}
    {@render previewTable(app.mal, (m) => m.sourceTitle, 'MyAnimeList')}
  {/if}
</div>
