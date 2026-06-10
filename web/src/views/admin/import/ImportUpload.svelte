<script>
  import { getContext } from 'svelte'
  const app = getContext('app')
</script>

<p class="label is-small has-text-grey">Select one or more files from your computer.</p>
<input class="ff-file" type="file" multiple onchange={(e) => app.onUploadPick(e)} disabled={app.uploading} />
{#if app.uploadError}<p class="has-text-danger">{app.uploadError}</p>{/if}
{#if app.uploadProgress.length}
  <ul class="ff-upload-list">
    {#each app.uploadProgress as p}
      <li class="ff-upload-item">
        <span class="ff-upload-name">{p.name}</span>
        <progress class="progress is-primary ff-upload-bar" max="100" value={p.pct}></progress>
        <span class="ff-upload-pct">{p.status === 'error' ? 'failed' : p.pct + '%'}</span>
      </li>
    {/each}
  </ul>
{/if}
<button class="button is-primary" disabled={!app.uploadFiles.length || app.uploading} onclick={() => app.startUpload()}>
  {app.uploading ? 'Uploading...' : `Upload ${app.uploadFiles.length} file${app.uploadFiles.length === 1 ? '' : 's'}`}
</button>
