import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

/** `dist` is build output and ignored, but Go embeds it with
 *  `//go:embed all:frontend/dist`. An empty directory makes that a compile
 *  error, so one tracked file has to survive — and Vite empties the directory
 *  on every build. Putting it back here means a fresh checkout compiles even
 *  before the frontend is built once. */
function keepEmbedDirectory() {
  return {
    name: 'conclave-keep-embed-directory',
    closeBundle() {
      writeFileSync(resolve(__dirname, 'dist/.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [react(), keepEmbedDirectory()],
  build: { target: 'esnext', chunkSizeWarningLimit: 1500 },
})
