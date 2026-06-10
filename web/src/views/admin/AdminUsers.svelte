<script>
  import { getContext } from 'svelte'
  import { fmtTime } from '../../lib/app.svelte.js'
  const app = getContext('app')
</script>

<h1 class="title is-4">Users</h1>
{#if app.usersError}<p class="has-text-danger">{app.usersError}</p>{/if}
<table class="table is-fullwidth is-hoverable">
  <thead>
    <tr><th>ID</th><th>Email</th><th>Alias</th><th>Role</th><th>Status</th><th>Last login</th><th></th></tr>
  </thead>
  <tbody>
    {#each app.users as u}
      <tr>
        <td>{u.id}</td>
        <td>{u.username}</td>
        <td>
          {#if app.editUserId === u.id}
            <input class="input is-small" bind:value={app.editUserAlias} onkeydown={(e) => e.key === 'Enter' && app.saveUserAlias(u)} />
          {:else}
            {u.alias || '-'}
          {/if}
        </td>
        <td>{u.admin ? 'admin' : 'user'}</td>
        <td>{u.blocked ? 'blocked' : 'active'}</td>
        <td>{fmtTime(u.lastLoginAt)}</td>
        <td class="ff-row-actions">
          {#if app.editUserId === u.id}
            <button class="button is-small is-primary" onclick={() => app.saveUserAlias(u)}>Save</button>
            <button class="button is-small is-ghost" onclick={() => (app.editUserId = 0)}>Cancel</button>
          {:else}
            <button class="button is-small" onclick={() => app.startEditUser(u)}>Edit</button>
            {#if u.username !== app.me.user}
              <button class="button is-small" onclick={() => app.patchUser(u, { admin: !u.admin })}>{u.admin ? 'Revoke admin' : 'Make admin'}</button>
              <button class="button is-small" class:is-danger={!u.blocked} onclick={() => app.patchUser(u, { blocked: !u.blocked })}>
                {u.blocked ? 'Unblock' : 'Block'}
              </button>
            {/if}
          {/if}
        </td>
      </tr>
    {/each}
    {#if app.users.length === 0}
      <tr><td colspan="7" class="has-text-grey">No users.</td></tr>
    {/if}
  </tbody>
</table>
<div class="ff-add-row">
  <input class="input" placeholder="Email" bind:value={app.newUserEmail} autocomplete="off" />
  <input class="input" placeholder="Alias" bind:value={app.newUserAlias} />
  <input class="input" type="password" placeholder="Password" bind:value={app.newUserPassword} autocomplete="new-password" />
  <label class="checkbox ff-add-check"><input type="checkbox" bind:checked={app.newUserAdmin} /> admin</label>
  <button class="button is-primary" disabled={!app.newUserReady} onclick={() => app.addUser()}>Add user</button>
</div>
