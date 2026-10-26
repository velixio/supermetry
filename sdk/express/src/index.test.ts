import { afterAll, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { createServer, type IncomingHttpHeaders, type Server } from "node:http";
import type { AddressInfo } from "node:net";

import express from "express";

import { Supermetry, supermetry } from "./index";

interface Received {
  headers: IncomingHttpHeaders;
  body: Record<string, unknown>;
}

let ingest: Server;
let ingestUrl: string;
const received: Received[] = [];

async function listen(server: Server): Promise<string> {
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
}

beforeAll(async () => {
  ingest = createServer((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      received.push({ headers: req.headers, body: JSON.parse(body) });
      res.writeHead(202).end();
    });
  });
  ingestUrl = await listen(ingest);
});

afterAll(() => {
  ingest.close();
});

beforeEach(() => {
  received.length = 0;
});

async function withApp(
  setup: (app: express.Express, client: Supermetry) => void,
  run: (base: string) => Promise<void>,
  options: Parameters<typeof supermetry>[0] = {},
): Promise<Supermetry> {
  const client = new Supermetry({ site: "site_x", host: ingestUrl });
  const app = express();
  app.set("trust proxy", true);
  app.use(supermetry({ client, ...options }));
  setup(app, client);
  const server = createServer(app);
  const base = await listen(server);
  try {
    await run(base);
    await client.flush();
  } finally {
    server.close();
  }
  return client;
}

describe("req.supermetry", () => {
  test("track is bound to the request UA, IP, URL and referrer", async () => {
    await withApp(
      (app) =>
        app.post("/api/signup", (req, res) => {
          req.supermetry.track("Signup", { props: { plan: "pro" }, profileId: "u1" });
          res.json({ ok: true });
        }),
      async (base) => {
        const res = await fetch(`${base}/api/signup?ref=x`, {
          method: "POST",
          headers: {
            "User-Agent": "Mozilla/5.0 (Macintosh) Safari/605.1.15",
            "X-Forwarded-For": "203.0.113.5",
            Host: "shop.supermetry.com",
            Referer: "https://shop.supermetry.com/pricing",
          },
        });
        expect(res.status).toBe(200);
      },
    );
    expect(received).toHaveLength(1);
    const [{ headers, body }] = received as [Received];
    expect(headers["user-agent"]).toBe("Mozilla/5.0 (Macintosh) Safari/605.1.15");
    expect(headers["x-forwarded-for"]).toBe("203.0.113.5");
    expect(body).toMatchObject({
      v: 1,
      site: "site_x",
      name: "Signup",
      url: "http://shop.supermetry.com/api/signup?ref=x",
      referrer: "https://shop.supermetry.com/pricing",
      props: { plan: "pro" },
      profile_id: "u1",
    });
  });

  test("per-call options override the request context; identify sends traits", async () => {
    await withApp(
      (app) =>
        app.get("/login", (req, res) => {
          req.supermetry.identify("u9", { email: "a@b.co" }, { url: "https://app.supermetry.com/welcome" });
          res.end("ok");
        }),
      async (base) => {
        await fetch(`${base}/login`, { headers: { "User-Agent": "Mozilla/5.0 Test" } });
      },
    );
    expect(received[0]!.body).toMatchObject({
      name: "identify",
      url: "https://app.supermetry.com/welcome",
      profile_id: "u9",
      traits: { email: "a@b.co" },
    });
  });

  test("identify keeps the profile ID for the rest of that request only", async () => {
    await withApp(
      (app) =>
        app.get("/login", (req, res) => {
          req.supermetry.identify("u5");
          req.supermetry.track("LoggedIn");
          res.end();
        }).get("/other", (req, res) => {
          req.supermetry.track("Anon");
          res.end();
        }),
      async (base) => {
        await fetch(`${base}/login`);
        await fetch(`${base}/other`);
      },
    );
    const byName = Object.fromEntries(received.map((r) => [r.body.name, r.body]));
    expect(byName.identify!.profile_id).toBe("u5");
    expect(byName.LoggedIn!.profile_id).toBe("u5");
    expect(byName.Anon!.profile_id).toBeUndefined();
  });

  test("getUrl and getProfileId hooks", async () => {
    await withApp(
      (app) =>
        app.get("/x", (req, res) => {
          req.supermetry.track("Viewed");
          res.end();
        }),
      async (base) => {
        await fetch(`${base}/x`);
      },
      { getUrl: (req) => `https://public.supermetry.com${req.originalUrl}`, getProfileId: () => "user_7" },
    );
    expect(received[0]!.body).toMatchObject({ url: "https://public.supermetry.com/x", profile_id: "user_7" });
  });
});

describe("trackPageviews", () => {
  const routes = (app: express.Express) => {
    app.get("/page", (_req, res) => {
      res.type("html").send("<h1>hi</h1>");
    });
    app.get("/api", (_req, res) => {
      res.json({});
    });
    app.get("/missing", (_req, res) => {
      res.status(404).type("html").send("nope");
    });
    app.post("/page", (_req, res) => {
      res.type("html").send("posted");
    });
  };
  const hitAll = async (base: string) => {
    for (const path of ["/page", "/api", "/missing"]) await (await fetch(base + path)).text();
    await (await fetch(base + "/page", { method: "POST" })).text();
    // Give the finish listeners a tick.
    await new Promise((r) => setTimeout(r, 20));
  };

  test("true: only GET 2xx HTML responses", async () => {
    await withApp(routes, hitAll, { trackPageviews: true });
    expect(received).toHaveLength(1);
    expect(received[0]!.body).toMatchObject({ name: "pageview" });
    expect(String(received[0]!.body.url)).toEndWith("/page");
  });

  test("function predicate decides", async () => {
    await withApp(routes, hitAll, { trackPageviews: (req) => req.path === "/api" });
    expect(received).toHaveLength(1);
    expect(String(received[0]!.body.url)).toEndWith("/api");
  });

  test("off by default", async () => {
    await withApp(routes, hitAll);
    expect(received).toHaveLength(0);
  });
});

test("middleware can create its own client from site/host", async () => {
  const app = express();
  app.use(supermetry({ site: "own", host: ingestUrl }));
  let client: Supermetry | undefined;
  app.get("/", (req, res) => {
    client = req.supermetry.client;
    req.supermetry.pageview();
    res.end();
  });
  const server = createServer(app);
  const base = await listen(server);
  await fetch(base + "/");
  await client!.flush();
  server.close();
  expect(received[0]!.body).toMatchObject({ site: "own", name: "pageview" });
});
