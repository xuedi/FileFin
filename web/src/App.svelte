<script>
  import { onMount } from 'svelte'
  import { api, Unauthorized } from './api.js'

  let booting = $state(true)
  let loggedIn = $state(false)
  let loginError = $state('')
  let username = $state('')
  let password = $state('')

  let categories = $state([])
  let activeCat = $state(null)
  let mediaList = $state([])
  let detail = $state(null)
  let currentFile = $state(0)

  onMount(async () => {
    try {
      categories = await api('/api/categories')
      loggedIn = true
    } catch (e) {
      if (!(e instanceof Unauthorized)) console.error(e)
    } finally {
      booting = false
    }
  })

  async function doLogin(e) {
    e.preventDefault()
    loginError = ''
    try {
      await api('/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      categories = await api('/api/categories')
      loggedIn = true
      password = ''
    } catch (e) {
      loginError = 'Invalid credentials'
    }
  }

  async function logout() {
    await api('/api/logout', { method: 'POST' })
    loggedIn = false
    categories = []
    activeCat = null
    mediaList = []
    detail = null
  }

  async function openCategory(name) {
    activeCat = name
    detail = null
    mediaList = await api('/api/categories/' + encodeURIComponent(name) + '/media')
  }

  async function openMedia(id) {
    detail = await api('/api/media/' + id)
    currentFile = 0
  }

  function fileLabel(f) {
    if (f.season || f.episode) return `S${f.season} E${f.episode} - ${f.name}`
    return f.name
  }
</script>

{#if booting}
  <p class="center">Loading…</p>
{:else if !loggedIn}
  <form class="login" onsubmit={doLogin}>
    <h1>FileFin</h1>
    <input placeholder="Username" bind:value={username} autocomplete="username" />
    <input type="password" placeholder="Password" bind:value={password} autocomplete="current-password" />
    <button type="submit">Sign in</button>
    {#if loginError}<p class="error">{loginError}</p>{/if}
  </form>
{:else}
  <header>
    <strong>FileFin</strong>
    <button class="link" onclick={logout}>Sign out</button>
  </header>
  <div class="layout">
    <nav>
      {#each categories as c}
        <button class:active={c.name === activeCat} onclick={() => openCategory(c.name)}>
          {c.name} <span class="count">{c.count}</span>
        </button>
      {/each}
    </nav>

    <main>
      {#if detail}
        <button class="link" onclick={() => (detail = null)}>← Back</button>
        {#if detail.hasBanner}
          <img class="banner" src={'/api/media/' + detail.id + '/banner'} alt="" />
        {/if}
        <h2>{detail.title} <span class="year">({detail.year})</span></h2>

        <video class="player" controls src={'/api/media/' + detail.id + '/file/' + currentFile}></video>

        {#if detail.files.length > 1}
          <div class="episodes">
            {#each detail.files as f}
              <button class:active={f.index === currentFile} onclick={() => (currentFile = f.index)}>
                {fileLabel(f)}
              </button>
            {/each}
          </div>
        {/if}

        {#if detail.description}<p>{detail.description}</p>{/if}
        {#if detail.tags.length}<p class="tags">{#each detail.tags as t}<span>{t}</span>{/each}</p>{/if}

        {#if detail.metadata.length}
          <table>
            <tbody>
              {#each detail.metadata as m}<tr><th>{m.key}</th><td>{m.value}</td></tr>{/each}
            </tbody>
          </table>
        {/if}

        {#if detail.actors.length}
          <h3>Cast</h3>
          <ul>{#each detail.actors as a}<li>{a}</li>{/each}</ul>
        {/if}

        {#if detail.plot}<h3>Plot</h3><p>{detail.plot}</p>{/if}
      {:else if activeCat}
        <div class="grid">
          {#each mediaList as m}
            <button class="card" onclick={() => openMedia(m.id)}>
              {#if m.hasPoster}
                <img src={'/api/media/' + m.id + '/poster'} alt={m.title} />
              {:else}
                <div class="noposter">{m.title}</div>
              {/if}
              <span>{m.title}</span>
              <span class="year">{m.year}</span>
            </button>
          {/each}
        </div>
      {:else}
        <p class="center">Pick a category.</p>
      {/if}
    </main>
  </div>
{/if}
