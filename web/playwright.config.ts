import { defineConfig, devices } from '@playwright/test'

const port = Number(process.env.PANEL_E2E_PORT ?? '18089')
const baseURL = `http://127.0.0.1:${port}`
const schema = `panel_e2e_${process.pid}`
const testDSN =
  process.env.PANEL_TEST_DSN ??
  'postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable'
const schemaDSN = new URL(testDSN)
schemaDSN.searchParams.set('options', `-csearch_path=${schema}`)
const resetFile = `/tmp/${schema}-reset.txt`
const sitesFile = `/tmp/${schema}-sites.json`
process.env.PANEL_TEST_DSN = testDSN
process.env.PANEL_E2E_SCHEMA = schema
process.env.PANEL_E2E_RESET_FILE = resetFile
process.env.PANEL_E2E_SITES_FILE = sitesFile

const serverCommand = String.raw`
set -eu
psql "$PANEL_TEST_DSN" -v ON_ERROR_STOP=1 \
  -c "drop schema if exists \"$PANEL_E2E_SCHEMA\" cascade" \
  -c "create schema \"$PANEL_E2E_SCHEMA\""
bun -e 'const fs=require("node:fs");const rows=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));fs.writeFileSync(process.argv[2],JSON.stringify(rows.map(({id,host,timezone})=>({id,org_id:1,public_id:host,timezone,retention_days:30,allowed_hosts:[host]}))))' \
  ../bench/gen/testdata/tiny.sites.json "$PANEL_E2E_SITES_FILE"
export PANEL_DATABASE_URL="$PANEL_E2E_DSN"
export PANEL_BASE_URL="$PANEL_E2E_BASE_URL"
export PANEL_SECRET_KEY="$PANEL_E2E_SECRET_KEY"
export PANEL_SITES_FILE="$PANEL_E2E_SITES_FILE"
../bin/panel migrate
../bin/panel load < ../bench/gen/testdata/tiny.ndjson
for site in $(seq 1 100); do
  ../bin/panel close-days --site "$site" --from 2026-09-28 --to 2026-09-30 >/dev/null
done
../bin/panel user create --email owner@panel.test --org Acme > "$PANEL_E2E_RESET_FILE"
export PANEL_LISTEN="127.0.0.1:$PANEL_E2E_PORT"
exec ../bin/panel serve
`

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  globalSetup: './e2e/fixtures.ts',
  outputDir: './test-results',
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL,
    colorScheme: 'light',
    reducedMotion: 'reduce',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: serverCommand,
    cwd: '.',
    env: {
      PANEL_TEST_DSN: testDSN,
      PANEL_E2E_DSN: schemaDSN.toString(),
      PANEL_E2E_SCHEMA: schema,
      PANEL_E2E_BASE_URL: baseURL,
      PANEL_E2E_PORT: String(port),
      PANEL_E2E_RESET_FILE: resetFile,
      PANEL_E2E_SITES_FILE: sitesFile,
      PANEL_E2E_SECRET_KEY: Buffer.alloc(32, 'e').toString('base64'),
    },
    url: `${baseURL}/readyz`,
    timeout: 180_000,
    reuseExistingServer: false,
  },
})
