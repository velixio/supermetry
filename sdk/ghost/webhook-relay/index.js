#!/usr/bin/env node
import { createRelayServer } from "./src/server.js";

const env = process.env;
let server;
try {
  server = createRelayServer({
    secret: env.GHOST_WEBHOOK_SECRET,
    siteUrl: env.GHOST_URL,
    site: env.SUPERMETRY_SITE,
    host: env.SUPERMETRY_HOST,
    path: env.WEBHOOK_PATH,
    signupEvent: env.SUPERMETRY_SIGNUP_EVENT,
    subscriptionEvent: env.SUPERMETRY_SUBSCRIPTION_EVENT,
    profileId: env.SUPERMETRY_PROFILE_ID,
    toleranceMs: env.SIGNATURE_TOLERANCE_SECONDS ? Number(env.SIGNATURE_TOLERANCE_SECONDS) * 1000 : undefined,
  });
} catch (error) {
  console.error(`supermetry-ghost-relay: ${error.message}`);
  process.exit(1);
}

const port = Number(env.PORT || 8787);
const host = env.BIND || "0.0.0.0";
server.listen(port, host, () => console.log(`supermetry-ghost-relay listening on http://${host}:${port}${env.WEBHOOK_PATH || "/ghost"}`));
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => server.close(() => process.exit(0)));
