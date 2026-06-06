import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Build outputs to ./dist, which the Go binary embeds. In dev, /api is proxied
// to the running backend so the SPA and API share an origin.
export default defineConfig({
  plugins: [svelte()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
