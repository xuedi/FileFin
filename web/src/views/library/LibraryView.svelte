<script>
  import { getContext } from 'svelte'
  import Home from './Home.svelte'
  import CategoryGrid from './CategoryGrid.svelte'
  import MediaDetail from './MediaDetail.svelte'
  const app = getContext('app')
</script>

{#if app.libMode === 'detail'}
  <MediaDetail />
{:else if app.libMode === 'category'}
  <CategoryGrid />
{:else}
  <Home />
{/if}
