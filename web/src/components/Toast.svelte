<script>
  import { getContext } from 'svelte'
  const app = getContext('app')
</script>

{#if app.toasts.length}
  <div class="ff-toasts">
    {#each app.toasts as t (t.id)}
      <div class="ff-toast" class:ff-toast-error={t.kind === 'error'} class:ff-toast-success={t.kind === 'success'}>
        <span>{t.text}</span>
        <button class="delete is-small" aria-label="dismiss" onclick={() => app.dismissToast(t.id)}></button>
      </div>
    {/each}
  </div>
{/if}
