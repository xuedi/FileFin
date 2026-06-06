import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

// keepGitkeep re-creates dist/.gitkeep after each build. emptyOutDir wipes the whole
// dist directory, which would otherwise delete the tracked placeholder that keeps
// web/dist/ present so `//go:embed all:dist` compiles even before a build has run.
function keepGitkeep() {
  return {
    name: 'keep-gitkeep',
    writeBundle(options) {
      writeFileSync(resolve(options.dir, '.gitkeep'), '')
    },
  }
}

// Build outputs to ./dist, which the Go binary embeds. In dev, /api is proxied
// to the running backend so the SPA and API share an origin.
export default defineConfig({
  plugins: [svelte(), keepGitkeep()],
  // hls.js is a single ~525 kB vendor lib, lazy-loaded only for transcode
  // playback, so it cannot be split further. Lift the limit past it; the initial
  // bundle is ~48 kB and stays well under.
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 600 },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
