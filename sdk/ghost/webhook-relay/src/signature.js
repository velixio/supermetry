import { createHmac, timingSafeEqual } from "node:crypto";

// Ghost signs webhooks with `X-Ghost-Signature: sha256=<hex>, t=<ms>`, where
// hex = HMAC-SHA256(secret, rawBody + t).
export function parseSignature(header) {
  if (typeof header !== "string") return null;
  let signature = "";
  let timestamp = NaN;
  for (const part of header.split(",")) {
    const [key, ...rest] = part.trim().split("=");
    const value = rest.join("=").trim();
    if (key === "sha256") signature = value.toLowerCase();
    else if (key === "t") timestamp = /^\d+$/.test(value) ? Number(value) : NaN;
  }
  if (!/^[0-9a-f]{64}$/.test(signature) || !Number.isSafeInteger(timestamp)) return null;
  return { signature, timestamp };
}

export function sign(rawBody, secret, timestamp) {
  const hex = createHmac("sha256", secret).update(Buffer.concat([toBuffer(rawBody), Buffer.from(String(timestamp))])).digest("hex");
  return `sha256=${hex}, t=${timestamp}`;
}

export function verifySignature(rawBody, header, secret, { now = Date.now(), toleranceMs = 5 * 60 * 1000 } = {}) {
  if (!secret) return false;
  const parsed = parseSignature(header);
  if (!parsed) return false;
  if (toleranceMs > 0 && Math.abs(now - parsed.timestamp) > toleranceMs) return false;
  const expected = createHmac("sha256", secret)
    .update(Buffer.concat([toBuffer(rawBody), Buffer.from(String(parsed.timestamp))]))
    .digest();
  return timingSafeEqual(expected, Buffer.from(parsed.signature, "hex"));
}

function toBuffer(body) {
  return Buffer.isBuffer(body) ? body : Buffer.from(String(body ?? ""), "utf8");
}
