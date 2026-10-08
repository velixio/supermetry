import { expect, test, type Page } from "@playwright/test";

type State = { uploads: Array<{ text: string; trigger: string | null; page: string | null; sequence: string | null }>; starts: number };

async function reset(page: Page) {
  await page.request.post("/test/reset");
}

async function state(page: Page): Promise<State> {
  return await (await page.request.get("/test/state")).json();
}

async function flush(page: Page) {
  await page.evaluate(() => dispatchEvent(new Event("pagehide")));
  await expect.poll(async () => (await state(page)).uploads.length).toBeGreaterThan(0);
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(navigator, "webdriver", { get: () => false }));
  await reset(page);
});

test("always masks password and card inputs when site masking is off", async ({ page }) => {
  await page.goto("/full");
  await page.locator("#password").fill("secret-password-927");
  await page.locator("#card").fill("4111111111111111");
  await page.locator("body").click();
  await flush(page);

  const payload = (await state(page)).uploads.map((upload) => upload.text).join("\n");
  expect(payload).not.toContain("secret-password-927");
  expect(payload).not.toContain("4111111111111111");
});

test("records ordinary text when site masking is off", async ({ page }) => {
  await page.goto("/full");
  await page.locator("#ordinary").fill("ordinary-visible-text-314");
  await page.locator("body").click();
  await flush(page);

  const payload = (await state(page)).uploads.map((upload) => upload.text).join("\n");
  expect(payload).toContain("ordinary-visible-text-314");
});

test("uploads the trigger buffer after an uncaught error", async ({ page }) => {
  await page.goto("/trigger");
  await page.evaluate(() => setTimeout(() => { throw new Error("trigger replay"); }, 0));

  await expect.poll(async () => (await state(page)).uploads.some((upload) => upload.trigger === "error")).toBe(true);
});

test("does not call start when saveData is enabled", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "connection", {
      configurable: true,
      value: { saveData: true, effectiveType: "4g" },
    });
  });
  await page.goto("/save-data");
  await page.waitForTimeout(250);

  expect((await state(page)).starts).toBe(0);
});

test("always consent waits for an explicit grant", async ({ page }) => {
  await page.goto("/consent-always");
  expect((await state(page)).starts).toBe(0);

  await page.evaluate(() => (window as typeof window & { panel?: { consent?(granted: boolean): void } }).panel?.consent?.(true));
  await expect.poll(async () => (await state(page)).starts).toBe(1);
});

test("manual trigger stays disabled even with a custom reason", async ({ page }) => {
  await page.goto("/trigger/manual-off");
  await expect.poll(async () => (await state(page)).starts).toBe(1);

  await page.evaluate(() => (window as typeof window & { panel?: { replay?: { trigger(reason?: string): void } } }).panel?.replay?.trigger("custom"));
  await page.waitForTimeout(250);
  expect((await state(page)).uploads).toHaveLength(0);
});

test("URL trigger reacts to SPA navigation", async ({ page }) => {
  await page.goto("/trigger/url-spa");
  await expect.poll(async () => (await state(page)).starts).toBe(1);

  await page.evaluate(() => history.pushState({}, "", "/checkout"));
  await expect.poll(async () => (await state(page)).uploads.some((upload) => upload.trigger === "url")).toBe(true);
});

test("SPA navigation starts a new replay page chunk", async ({ page }) => {
  await page.goto("/full");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await page.locator("#ordinary").fill("first page");
  await page.evaluate(() => history.pushState({}, "", "/next-page"));
  await page.locator("#ordinary").fill("second page");
  await flush(page);

  const uploads = (await state(page)).uploads;
  expect(uploads.length).toBeGreaterThanOrEqual(2);
  expect(uploads[0].page).toBe("1");
  expect(uploads[1].page).toBe("1");
});

test("trigger ring keeps the preceding minute across a checkpoint", async ({ page }) => {
  await page.clock.install();
  await page.goto("/trigger");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await page.locator("#ordinary").fill("before-wrap-history");
  await page.locator("body").click();

  await page.clock.fastForward(61_000);
  await page.locator("#ordinary").fill("after-wrap-event");
  await page.locator("body").click();
  await page.evaluate(() => (window as typeof window & { panel?: { replay?: { trigger(): void } } }).panel?.replay?.trigger());
  await expect.poll(async () => (await state(page)).uploads.length).toBeGreaterThan(0);

  const payload = (await state(page)).uploads.map((upload) => upload.text).join("\n");
  expect(payload).toContain("before-wrap-history");
  expect(payload).toContain("after-wrap-event");
  const events = payload.trim().split("\n").map((line) => JSON.parse(line) as { type: number });
  expect(events.filter((event) => event.type === 2).length).toBeGreaterThanOrEqual(2);
});

test("hidden visibility preflushes before pagehide", async ({ page }) => {
  await page.goto("/full");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await page.locator("#ordinary").fill("visibility-preflush");
  await page.locator("body").click();

  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, value: true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(async () => (await state(page)).uploads.some((upload) => upload.text.includes("visibility-preflush"))).toBe(true);
});

test("runtime opt-out stops replay without uploading buffered input", async ({ page }) => {
  await page.goto("/full");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await page.locator("#ordinary").fill("discard-after-opt-out");
  await page.locator("body").click();

  await page.evaluate(() => (window as typeof window & { panel?: { optOut(): void } }).panel?.optOut());
  await page.evaluate(() => dispatchEvent(new Event("pagehide")));
  await page.waitForTimeout(250);
  expect((await state(page)).uploads).toHaveLength(0);
});

test("consent withdrawal stops an active replay", async ({ page }) => {
  await page.goto("/consent-always");
  await page.evaluate(() => (window as typeof window & { panel?: { consent(granted: boolean): void } }).panel?.consent(true));
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await expect.poll(() => page.evaluate(() => typeof (window as typeof window & { panel?: { _replayStop?: unknown } }).panel?._replayStop)).toBe("function");
  await page.locator("#ordinary").fill("discard-after-withdrawal");
  await page.locator("body").click();

  await page.evaluate(() => (window as typeof window & { panel?: { consent(granted: boolean): void } }).panel?.consent(false));
  await page.evaluate(() => dispatchEvent(new Event("pagehide")));
  await page.waitForTimeout(250);
  expect((await state(page)).uploads).toHaveLength(0);
});

test("idle timeout flushes the pending tail before stopping", async ({ page }) => {
  await page.clock.install();
  await page.goto("/full");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await expect.poll(() => page.evaluate(() => typeof (window as typeof window & { panel?: { _replayStop?: unknown } }).panel?._replayStop)).toBe("function");
  await page.locator("#ordinary").fill("idle-tail-event");
  await page.locator("body").click();

  await page.clock.fastForward(121_000);
  await expect.poll(async () => (await state(page)).uploads.some((upload) => upload.text.includes("idle-tail-event"))).toBe(true);
});

test("pagehide stops an untriggered ring without uploading it later", async ({ page }) => {
  await page.goto("/trigger");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await page.evaluate(() => dispatchEvent(new Event("pagehide")));
  await page.evaluate(() => (window as typeof window & { panel?: { replay?: { trigger(): void } } }).panel?.replay?.trigger());
  await page.waitForTimeout(250);
  expect((await state(page)).uploads).toHaveLength(0);
});

test("oversized trigger snapshot fails closed without recorder errors", async ({ page }) => {
  const errors: Error[] = [];
  page.on("pageerror", (error) => errors.push(error));
  await page.goto("/trigger/huge");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await page.waitForTimeout(250);
  await page.evaluate(() => (window as typeof window & { panel?: { replay?: { trigger(): void } } }).panel?.replay?.trigger());
  await page.waitForTimeout(250);
  await page.evaluate(() => history.pushState({}, "", "/after-oversized-stop"));
  await page.waitForTimeout(50);

  expect(errors).toHaveLength(0);
  expect((await state(page)).uploads).toHaveLength(0);
});

test("consent re-grant starts a fresh replay", async ({ page }) => {
  await page.goto("/consent-always");
  await page.evaluate(() => (window as typeof window & { panel?: { consent(granted: boolean): void } }).panel?.consent(true));
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await expect.poll(() => page.evaluate(() =>
    typeof (window as typeof window & { panel?: { _replayStop?: unknown } }).panel?._replayStop,
  )).toBe("function");
  await page.evaluate(() => (window as typeof window & { panel?: { consent(granted: boolean): void } }).panel?.consent(false));
  await expect.poll(() => page.evaluate(() =>
    Boolean((window as typeof window & { panel?: { _replay?: unknown } }).panel?._replay),
  )).toBe(false);
  await page.evaluate(() => (window as typeof window & { panel?: { consent(granted: boolean): void } }).panel?.consent(true));

  await expect.poll(async () => (await state(page)).starts).toBe(2);
});

test("opt-in starts a fresh replay after runtime opt-out", async ({ page }) => {
  await page.goto("/full");
  await expect.poll(async () => (await state(page)).starts).toBe(1);
  await expect.poll(() => page.evaluate(() =>
    typeof (window as typeof window & { panel?: { _replayStop?: unknown } }).panel?._replayStop,
  )).toBe("function");
  await page.evaluate(() => (window as typeof window & { panel?: { optOut(): void; optIn(): void } }).panel?.optOut());
  await expect.poll(() => page.evaluate(() =>
    Boolean((window as typeof window & { panel?: { _replay?: unknown } }).panel?._replay),
  )).toBe(false);
  await page.evaluate(() => (window as typeof window & { panel?: { optOut(): void; optIn(): void } }).panel?.optIn());

  await expect.poll(async () => (await state(page)).starts).toBe(2);
});
