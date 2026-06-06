<script>
  // Renders a Font Awesome Free icon definition as an inline SVG. Only the icon paths we
  // import are bundled (tree-shaken), and they ship inside the binary - nothing is fetched
  // from a CDN at runtime. `icon` is a definition like `faHeart`; its `.icon` field is
  // [width, height, ligatures, unicode, pathData].
  let { icon, size = '1em', color = 'currentColor', title } = $props()

  const width = $derived(icon.icon[0])
  const height = $derived(icon.icon[1])
  const path = $derived(icon.icon[4])
  const d = $derived(Array.isArray(path) ? path.join(' ') : path)
</script>

<svg
  xmlns="http://www.w3.org/2000/svg"
  viewBox="0 0 {width} {height}"
  width={size}
  height={size}
  fill={color}
  role={title ? 'img' : 'presentation'}
  aria-hidden={title ? undefined : 'true'}
  aria-label={title}
>
  {#if title}<title>{title}</title>{/if}
  <path {d} />
</svg>
