import { describe, expect, test } from "bun:test";
import { createClient } from "../src/index.js";

const host = process.env.SUPERMETRY_E2E_HOST;
const appId = process.env.SUPERMETRY_E2E_APP_ID;
const key = process.env.SUPERMETRY_E2E_KEY;
const run = host && appId && key ? describe : describe.skip;

// Opt-in: runs against a real server. Set SUPERMETRY_E2E_HOST, _APP_ID and _KEY.
run("e2e against a real server", () => {
  test("session, exception and crash are accepted with no drops", async () => {
    const replies: any[] = [];
    const drops: any[] = [];
    const client = createClient({
      appId: appId!,
      ingestKey: key!,
      host: host!,
      release: "0.0.1-e2e",
      environment: "e2e",
      installId: process.env.SUPERMETRY_E2E_INSTALL_ID ?? "e2e-install-ts",
      flushIntervalMs: 0,
      onDrop: (events, reason) => void drops.push({ n: events.length, reason }),
      transport: async (req) => {
        const res = await fetch(req.url, { method: "POST", headers: req.headers, body: req.body });
        const body = await res.text();
        replies.push({ status: res.status, body });
        return { status: res.status, body };
      },
    });
    client.startSession();
    client.captureException(new TypeError(process.env.SUPERMETRY_E2E_SECRET ?? "e2e-secret-message-ts"), { handled: true });
    client.captureCrash({ errorType: "SIGSEGV", exitReason: "signal", signal: "SIGSEGV", exitCode: 139 });
    await client.flush();
    expect(replies.length).toBe(1);
    expect(replies[0].status).toBe(202);
    const parsed = JSON.parse(replies[0].body);
    expect(parsed.accepted).toBe(3);
    expect(parsed.dropped ?? []).toEqual([]);
    expect(drops).toEqual([]);
  });
});
