import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  server: {
    // wails3 dev passes --port (build/Taskfile.yml); the Go side proxies to it.
    host: '127.0.0.1',
    strictPort: true,
  },
  test: {
    environment: 'jsdom',
    // Vitest empties CSS imports; native.test.ts reads tokens.css?raw to check the motion and focus rules.
    css: { include: [/tokens\.css/] },
    alias: {
      // The real runtime calls the Go side on import.
      '@wailsio/runtime': fileURLToPath(new URL('./src/test/wails-runtime.ts', import.meta.url)),
    },
  },
})
