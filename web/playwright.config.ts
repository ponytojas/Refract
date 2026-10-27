import { defineConfig } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { randomBytes } from 'node:crypto'

// Browser tests against the real binary (run `make build` first; needs tectonic on PATH),
// with a scripted model (e2e/fake-model.mjs) for the assistant.
// Uses the installed Google Chrome, so no browser download is needed.
const port = 18090
const dataDir = mkdtempSync(join(tmpdir(), 'refract-e2e-'))

export default defineConfig({
  testDir: 'e2e',
  timeout: 120_000,
  use: { baseURL: `http://127.0.0.1:${port}`, channel: 'chrome', viewport: { width: 1500, height: 850 } },
  webServer: [
    { command: 'node e2e/fake-model.mjs', url: 'http://127.0.0.1:18091/api/v1/models' },
    {
      command: '../refract',
      url: `http://127.0.0.1:${port}/healthz`,
      env: {
        REFRACT_AUTH: 'none',
        REFRACT_ADDR: `127.0.0.1:${port}`,
        REFRACT_DATA_DIR: dataDir,
        REFRACT_SECRET_KEY: randomBytes(32).toString('base64'),
      },
    },
  ],
})
