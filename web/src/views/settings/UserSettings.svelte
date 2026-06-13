<script>
  import { getContext } from 'svelte'

  const app = getContext('app')
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

<!-- The MyDramaList integration section attaches below in a follow-up. -->
