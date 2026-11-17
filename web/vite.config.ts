import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const target = process.env.SERVER_URL ?? 'http://localhost:8080'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    // The browser only ever talks to the Vite origin. Host is preserved
    // (changeOrigin: false) so the Go websocket origin check passes.
    proxy: {
      '/api': target,
      '/ws': { target, ws: true },
    },
  },
})
