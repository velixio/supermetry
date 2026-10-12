import { defineConfig, devices } from "@playwright/test";

// Existing feature/auth fixtures deliberately exercise identified reads and replay.
// Anonymous policy is verified separately with e2e/privacy.spec.ts.
process.env.SUPERMETRY_TRACKING_MODE ??= "identified";
process.env.SUPERMETRY_REPLAY_ENABLED ??= "true";

const port = Number(process.env.SUPERMETRY_E2E_PORT);
if (!Number.isSafeInteger(port) || port < 1 || port > 65_535) {
  throw new Error("SUPERMETRY_E2E_PORT must be set to an isolated TCP port");
}
const baseURL = `http://127.0.0.1:${port}`;
const schema = `supermetry_e2e_${process.pid}`;
const testDSN =
  process.env.SUPERMETRY_TEST_DSN ??
  "postgres://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable";
const schemaDSN = new URL(testDSN);
schemaDSN.searchParams.set("options", `-csearch_path=${schema}`);
const resetFile = `/tmp/${schema}-reset.txt`;
const sitesFile = `/tmp/${schema}-sites.json`;
const serverPIDFile = `/tmp/${schema}-server.pid`;
process.env.SUPERMETRY_TEST_DSN = testDSN;
process.env.SUPERMETRY_E2E_SCHEMA = schema;
process.env.SUPERMETRY_E2E_RESET_FILE = resetFile;
process.env.SUPERMETRY_E2E_SITES_FILE = sitesFile;
process.env.SUPERMETRY_E2E_SERVER_PID_FILE = serverPIDFile;

const serverCommand = String.raw`
set -eu
psql "$SUPERMETRY_TEST_DSN" -v ON_ERROR_STOP=1 \
  -c "drop schema if exists \"$SUPERMETRY_E2E_SCHEMA\" cascade" \
  -c "create schema \"$SUPERMETRY_E2E_SCHEMA\""
bun -e 'const fs=require("node:fs");const rows=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));fs.writeFileSync(process.argv[2],JSON.stringify(rows.map(({id,host,timezone})=>({id,org_id:1,public_id:host,timezone,retention_days:30,allowed_hosts:[host],revenue_currency:"USD",revenue_currency_state:"configured"}))))' \
  ../bench/gen/testdata/tiny.sites.json "$SUPERMETRY_E2E_SITES_FILE"
export SUPERMETRY_DATABASE_URL="$SUPERMETRY_E2E_DSN"
export SUPERMETRY_BASE_URL="$SUPERMETRY_E2E_BASE_URL"
export SUPERMETRY_SECRET_KEY="$SUPERMETRY_E2E_SECRET_KEY"
export SUPERMETRY_SITES_FILE="$SUPERMETRY_E2E_SITES_FILE"
../bin/supermetry migrate
../bin/supermetry load < ../bench/gen/testdata/tiny.ndjson
for site in $(seq 1 100); do
  ../bin/supermetry close-days --site "$site" --from 2026-09-28 --to 2026-09-30 >/dev/null
done
../bin/supermetry user create --email owner@supermetry.test --org Acme > "$SUPERMETRY_E2E_RESET_FILE"
export SUPERMETRY_LISTEN="127.0.0.1:$SUPERMETRY_E2E_PORT"
echo "$$" > "$SUPERMETRY_E2E_SERVER_PID_FILE"
exec ../bin/supermetry serve
`;

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  globalSetup: "./e2e/fixtures.ts",
  outputDir: "./test-results",
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL,
    colorScheme: "light",
    reducedMotion: "reduce",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        ...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
          ? {
              launchOptions: {
                executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH,
              },
            }
          : {}),
      },
    },
  ],
  webServer: {
    command: serverCommand,
    cwd: ".",
    env: {
      SUPERMETRY_TRACKING_MODE: process.env.SUPERMETRY_TRACKING_MODE,
      SUPERMETRY_REPLAY_ENABLED: process.env.SUPERMETRY_REPLAY_ENABLED,
      SUPERMETRY_TEST_DSN: testDSN,
      SUPERMETRY_E2E_DSN: schemaDSN.toString(),
      SUPERMETRY_E2E_SCHEMA: schema,
      SUPERMETRY_E2E_BASE_URL: baseURL,
      SUPERMETRY_E2E_PORT: String(port),
      SUPERMETRY_E2E_RESET_FILE: resetFile,
      SUPERMETRY_E2E_SITES_FILE: sitesFile,
      SUPERMETRY_E2E_SERVER_PID_FILE: serverPIDFile,
      SUPERMETRY_E2E_SECRET_KEY: Buffer.alloc(32, "e").toString("base64"),
      SUPERMETRY_E2E_LOGIN_LIMIT: "100",
    },
    url: `${baseURL}/readyz`,
    timeout: 300_000,
    reuseExistingServer: false,
  },
});
