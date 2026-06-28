<script>
  import { getContext } from 'svelte'
  import GithubLink from '../components/GithubLink.svelte'
  const app = getContext('app')
</script>

<form class="box ff-auth" onsubmit={(e) => app.doLogin(e)}>
  <h1 class="title is-4 has-text-centered">FileFin</h1>
  <div class="field">
    <div class="control">
      <input class="input" placeholder="Username" bind:value={app.luser} autocomplete="username" />
    </div>
  </div>
  <div class="field">
    <div class="control">
      <input class="input" type="password" placeholder="Password" bind:value={app.lpass} autocomplete="current-password" />
    </div>
  </div>
  <div class="field">
    <button type="submit" class="button is-primary is-fullwidth">Log in</button>
  </div>
  {#if app.loginError}<p class="has-text-danger">{app.loginError}</p>{/if}
</form>
<div class="ff-auth-foot has-text-centered">
  <GithubLink />
</div>
