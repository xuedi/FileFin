<script>
  import { getContext } from 'svelte'
  import ProgressBar from '../../components/ProgressBar.svelte'
  const app = getContext('app')
</script>

<h1 class="title is-4">Progress</h1>

<div class="columns">
  <div class="column is-half">
    <h2 class="title is-6 ff-prog-section">Runs</h2>
    {#if app.runs.length === 0}
      <p class="has-text-grey">Nothing running.</p>
    {:else}
      {#each app.runs as run (run.kind)}
        <div class="ff-run">
          <p class="ff-run-head">
            <strong>{run.label}</strong>
            {#if run.detail}<span class="has-text-grey"> - {run.detail}</span>{/if}
          </p>
          <ProgressBar value={run.done} max={run.total || 1} />
          <p class="has-text-grey is-size-7 ff-prog-waiting">{run.done} / {run.total} done</p>
        </div>
      {/each}
    {/if}
  </div>

  <div class="column is-half">
    <h2 class="title is-6 ff-prog-section">Activity</h2>
    {#if app.activity.length === 0}
      <p class="has-text-grey">Nothing running.</p>
    {:else}
      <ul class="ff-activity">
        {#each app.activity as item (item.key)}
          <li class="ff-activity-item">
            <div class="ff-activity-head">
              <span class="tag is-small ff-activity-kind">{item.kind}</span>
              <span class="ff-activity-title">{item.title}</span>
              {#if item.percent < 0 && item.state}
                <span class="has-text-grey is-size-7 ff-activity-pct">{item.state}</span>
              {/if}
            </div>
            {#if item.percent >= 0}
              <ProgressBar value={item.percent} />
            {/if}
            {#if item.detail}
              <p class="has-text-grey is-size-7 ff-activity-detail">{item.detail}</p>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
