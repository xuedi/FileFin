import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

// emptyOutDir wipes dist on each build, which would delete the tracked .gitkeep that
// keeps web/dist/ present so `//go:embed all:dist` compiles before a build has run.
// Re-create it after every bundle.
function keepGitkeep() {
  return {
    name: 'keep-gitkeep',
    writeBundle(options) {
      writeFileSync(resolve(options.dir, '.gitkeep'), '')
    },
  }
}

// Build outputs to ./dist, which the Go binary embeds. In dev, /api is proxied to the
// running backend so the SPA and API share an origin.
export default defineConfig({
  plugins: [svelte(), keepGitkeep()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: { '/api': 'http://localhost:8080' },
  },
})
