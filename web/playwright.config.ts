import { defineConfig } from '@playwright/test'

// The tests run against their own Go server (empty throwaway database) and
// Vite dev server on ports that do not clash with `make dev`.
const goPort = 8181
const webPort = 5273

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: {
    baseURL: `http://localhost:${webPort}`,
    trace: 'retain-on-failure',
  },
  webServer: [
    {
      command: 'go run .',
      cwd: '../server',
      env: {
        PORT: String(goPort),
        DB_PATH: `${process.env.TMPDIR ?? '/tmp'}/markdown-editor-e2e-${process.pid}.db`,
        // Small enough that one large paste makes the server unable to replay
        // what a disconnected client missed (reconnect.spec.ts), large enough
        // that a few keystrokes are replayable.
        RESUME_LOG_BYTES: '2048',
      },
      url: `http://localhost:${goPort}/api/entries`,
      reuseExistingServer: false,
    },
    {
      command: `npm run dev -- --port ${webPort} --strictPort`,
      env: { SERVER_URL: `http://localhost:${goPort}` },
      url: `http://localhost:${webPort}`,
      reuseExistingServer: false,
    },
  ],
})
