<script>
  // Bulma progress bar with an inline percent label. Pass value/max directly.
  let { value = 0, max = 100 } = $props()
  const pctText = $derived(Math.round((value / (max || 100)) * 100) + '%')
</script>

<span class="ff-progress-wrap">
  <progress class="progress is-primary ff-progress" {value} {max}></progress>
  <span class="ff-progress-pct">{pctText}</span>
</span>
