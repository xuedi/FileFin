<script>
  import { getContext } from 'svelte'
  const app = getContext('app')
</script>

<h1 class="title is-4">Choose a media format</h1>
<p class="content ff-settings-intro">
  This choice is <strong>permanent</strong>. It dictates how and in what format media files are
  organized in the app after import. The on-disk layout is always
  <code>{'{dataDir}/{category}/{mediafolder}/'}</code>; the format sets the naming style of the
  media folder and its files.
</p>
<div class="ff-format-boxes">
  {#each app.formatBoxes as b}
    <button
      type="button"
      class="box ff-format-box"
      class:ff-selected={app.formatChoice === b.id}
      onclick={() => (app.formatChoice = b.id)}>
      <span class="has-text-weight-semibold">{b.title}</span>
      <span class="has-text-grey is-size-7">{b.desc}</span>
      <code>{b.movie}</code>
      <code>{b.episode}</code>
    </button>
  {/each}
</div>
<button class="button is-primary" disabled={!app.formatChoice} onclick={() => app.selectFormat()}>Permanently select</button>
