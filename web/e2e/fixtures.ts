import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, rmSync } from "node:fs";

import {
  expect,
  request as playwrightRequest,
  test as base,
  type APIRequestContext,
  type APIResponse,
  type FullConfig,
  type Page,
} from "@playwright/test";

const email = "owner@supermetry.test";
const password = "supermetry-e2e-password";

export type Owner = {
  schema: string;
  email: string;
  password: string;
  orgId: number;
  orgSlug: string;
  siteId: number;
  sitePublicID: string;
  siteSlug: string;
  shareSlug: string;
  apiHeaders: Record<string, string>;
  storageState: Awaited<ReturnType<APIRequestContext["storageState"]>>;
  login(page: Page, next?: string): Promise<void>;
};

type SerializedOwner = Omit<Owner, "login" | "storageState">;
type WorkerOwner = Omit<Owner, "login">;

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required for the e2e suite`);
  return value;
}

function value<T>(
  record: Record<string, unknown>,
  snake: string,
  title: string,
): T {
  const result = record[snake] ?? record[title];
  if (result === undefined)
    throw new Error(`missing ${snake} in e2e API response`);
  return result as T;
}

export function fmtCompact(input: number): string {
  return Math.abs(input) >= 10_000
    ? new Intl.NumberFormat("en-US", {
        notation: "compact",
        maximumFractionDigits: 1,
      }).format(input)
    : new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 }).format(
        input,
      );
}

async function responseJSON<T>(response: APIResponse): Promise<T> {
  const body = await response.text();
  expect(response.ok(), body).toBe(true);
  return JSON.parse(body) as T;
}

export default async function globalSetup(config: FullConfig) {
  const baseURL = String(config.projects[0]?.use.baseURL);
  const resetURL = readFileSync(
    required("SUPERMETRY_E2E_RESET_FILE"),
    "utf8",
  ).trim();
  // Reset links are /reset/{token}; older builds printed /password-reset?token=.
  const parsed = new URL(resetURL);
  const token =
    parsed.searchParams.get("token") ??
    (parsed.pathname.startsWith("/reset/")
      ? decodeURIComponent(parsed.pathname.slice("/reset/".length))
      : null);
  if (!token)
    throw new Error("supermetry user create did not return a reset token");

  const api = await playwrightRequest.newContext({
    baseURL,
    extraHTTPHeaders: { Origin: baseURL },
  });
  const reset = await api.post("/api/private/auth/password-reset/confirm", {
    data: { token, password },
  });
  const resetBody = await reset.text();
  expect(reset.ok(), resetBody).toBe(true);

  const login = await api.post("/api/private/auth/login", {
    data: { email, password, device_label: "Playwright setup" },
  });
  const loginBody = await login.text();
  expect(login.ok(), loginBody).toBe(true);

  const state = await api.storageState();
  const csrf = state.cookies.find(
    (cookie) => cookie.name === "supermetry_csrf",
  )?.value;
  if (!csrf) throw new Error("login did not set the CSRF cookie");
  const cookie = state.cookies
    .map((item) => `${item.name}=${item.value}`)
    .join("; ");
  const authHeaders = { Cookie: cookie, "X-CSRF-Token": csrf };

  const organizations = await api.get("/api/private/orgs", {
    headers: authHeaders,
  });
  const organizationBody = await responseJSON<{
    data: Array<Record<string, unknown>>;
  }>(organizations);
  const membership = organizationBody.data[0];
  if (!membership) throw new Error("owner has no organization");
  const orgId = value<number>(membership, "org_id", "OrgID");
  const orgSlug = value<string>(membership, "org_slug", "OrgSlug");
  const apiHeaders = { ...authHeaders, "X-Supermetry-Org": String(orgId) };

  const createdSite = await api.post("/api/private/sites", {
    headers: apiHeaders,
    data: {
      name: "Site 1",
      hosts: ["site1.test"],
      timezone: "America/New_York",
      retention_days: 0,
      revenue_currency: "USD",
    },
  });
  const site = await responseJSON<Record<string, unknown>>(createdSite);
  const siteId = value<number>(site, "id", "ID");
  const sitePublicID = value<string>(site, "public_id", "PublicID");
  const siteSlug = value<string>(site, "slug", "Slug");

  const createdShare = await api.post(`/api/private/sites/${siteId}/shares`, {
    headers: apiHeaders,
    data: {
      password: '',
      config: {
        sections: ['kpis', 'chart', 'pages', 'sources', 'locations', 'devices', 'where', 'weekly_rhythm', 'goals'],
        filters: [],
        date: { mode: 'rolling', days: 30, max_days: 30 },
        theme: 'system',
        embed: { enabled: false, allowed_origins: [], confirm_wildcard: false },
      },
    },
  })
  const share = await responseJSON<Record<string, unknown>>(createdShare)
  const shareSlug = value<string>(share, 'slug', 'Slug')

  const owner: SerializedOwner = {
    schema: required("SUPERMETRY_E2E_SCHEMA"),
    email,
    password,
    orgId,
    orgSlug,
    siteId,
    sitePublicID,
    siteSlug,
    shareSlug,
    apiHeaders,
  };
  process.env.SUPERMETRY_E2E_OWNER = JSON.stringify(owner);
  mkdirSync("e2e/screenshots", { recursive: true });
  await api.dispose();

  return async () => {
    const testDSN = required("SUPERMETRY_TEST_DSN");
    const schema = required("SUPERMETRY_E2E_SCHEMA");
    const serverPIDFile = required("SUPERMETRY_E2E_SERVER_PID_FILE");
    const serverPID = Number(readFileSync(serverPIDFile, "utf8").trim());
    if (!Number.isSafeInteger(serverPID) || serverPID <= 1)
      throw new Error("invalid E2E server PID");
    try {
      process.kill(serverPID, "SIGTERM");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error;
    }
    const shutdownDeadline = Date.now() + 5_000;
    while (Date.now() < shutdownDeadline) {
      try {
        process.kill(serverPID, 0);
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code === "ESRCH") break;
        throw error;
      }
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    try {
      process.kill(serverPID, 0);
      throw new Error("E2E server did not stop before schema cleanup");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error;
    }
    execFileSync("psql", [
      testDSN,
      "-v",
      "ON_ERROR_STOP=1",
      "-c",
      `drop schema if exists "${schema}" cascade`,
    ]);
    rmSync(required("SUPERMETRY_E2E_RESET_FILE"), { force: true });
    rmSync(required("SUPERMETRY_E2E_SITES_FILE"), { force: true });
    rmSync(serverPIDFile, { force: true });
  };
}

export const test = base.extend<{ owner: Owner }, { workerOwner: WorkerOwner }>(
  {
    workerOwner: [
      async ({ playwright }, use, workerInfo) => {
        const serialized = JSON.parse(
          required("SUPERMETRY_E2E_OWNER"),
        ) as SerializedOwner;
        const baseURL = String(workerInfo.project.use.baseURL);
        const api = await playwright.request.newContext({
          baseURL,
          extraHTTPHeaders: { Origin: baseURL },
        });
        const login = await api.post("/api/private/auth/login", {
          data: {
            email: serialized.email,
            password: serialized.password,
            device_label: `Playwright worker ${workerInfo.workerIndex}`,
          },
        });
        expect(login.ok(), await login.text()).toBe(true);
        const storageState = await api.storageState();
        const csrf = storageState.cookies.find(
          (cookie) => cookie.name === "supermetry_csrf",
        )?.value;
        if (!csrf)
          throw new Error("worker API login did not set the CSRF cookie");
        await use({
          ...serialized,
          apiHeaders: serialized.apiHeaders,
          storageState,
        });
        await api.dispose();
      },
      { scope: "worker" },
    ],
    owner: async ({ workerOwner, playwright }, use, testInfo) => {
      const baseURL = String(testInfo.project.use.baseURL);
      const api = await playwright.request.newContext({
        baseURL,
        extraHTTPHeaders: { Origin: baseURL },
      });
      const response = await api.post("/api/private/auth/login", {
        data: {
          email: workerOwner.email,
          password: workerOwner.password,
          device_label: `Playwright test ${testInfo.title}`,
        },
      });
      expect(response.ok(), await response.text()).toBe(true);
      const storageState = await api.storageState();
      const csrf = storageState.cookies.find(
        (cookie) => cookie.name === "supermetry_csrf",
      )?.value;
      if (!csrf) throw new Error("test API login did not set the CSRF cookie");
      const cookie = storageState.cookies
        .map((item) => `${item.name}=${item.value}`)
        .join("; ");
      await use({
        ...workerOwner,
        apiHeaders: { Cookie: cookie, "X-CSRF-Token": csrf },
        storageState,
        async login(page, next = "/") {
          const destination = new URL(next, "http://supermetry.test");
          const reachedDestination = (url: URL) =>
            next === "/"
              ? url.pathname !== "/login"
              : url.pathname === destination.pathname &&
                [...destination.searchParams].every(
                  ([name, value]) => url.searchParams.get(name) === value,
                );
          await page.context().addCookies(storageState.cookies);
          await page.goto(next);
          await page.waitForURL(reachedDestination);
        },
      });
      await api.dispose();
    },
  },
);

export { expect } from "@playwright/test";
