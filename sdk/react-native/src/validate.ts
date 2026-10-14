// Local validation mirroring internal/ingest/payload.go (same rules as @supermetry/node).
// Anything the server would reject for the whole request is dropped or reported here.

export type PropValue = string | number | boolean;
export type Props = Record<string, PropValue | null | undefined>;
export type Traits = Record<string, string | number | boolean | null | undefined>;

export interface Revenue {
  /** Positive amount, at most 6 decimals, e.g. `49.9` or `"49.90"`. */
  amount: string | number;
  /** ISO 4217 code; must match the site currency (or be omitted). */
  currency?: string;
}

export const MAX_PROPS = 30;
export const MAX_KEY_BYTES = 300;
export const MAX_VALUE_BYTES = 2000;
export const MAX_URL_BYTES = 2000;
export const MAX_NAME_BYTES = 300;
export const MAX_BODY_BYTES = 8 * 1024;
export const MAX_PROFILE_ID_CHARS = 128;
export const MAX_TRAITS = 20;
export const MAX_TRAIT_KEY_CHARS = 64;
export const MAX_TRAIT_VALUE_CHARS = 256;

/** UTF-8 byte length without TextEncoder (missing on older Hermes). */
export function byteLength(value: string): number {
  let bytes = 0;
  for (const ch of value) {
    const code = ch.codePointAt(0)!;
    bytes += code < 0x80 ? 1 : code < 0x800 ? 2 : code < 0x10000 ? 3 : 4;
  }
  return bytes;
}

export function charLength(value: string): number {
  let count = 0;
  for (const _ of value) count++;
  return count;
}

/** Truncates to at most `max` UTF-8 bytes without splitting a code point. */
export function truncateBytes(value: string, max: number): string {
  if (byteLength(value) <= max) return value;
  let out = "";
  let size = 0;
  for (const ch of value) {
    const n = byteLength(ch);
    if (size + n > max) break;
    out += ch;
    size += n;
  }
  return out;
}

export function validName(name: unknown): name is string {
  return typeof name === "string" && name.length > 0 && byteLength(name) <= MAX_NAME_BYTES;
}

/** Absolute http(s) URL without userinfo, at most 2000 bytes. */
export function validEventUrl(value: unknown): value is string {
  if (typeof value !== "string" || value === "" || byteLength(value) > MAX_URL_BYTES) return false;
  try {
    const parsed = new URL(value);
    return (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      parsed.hostname !== "" && parsed.username === "" && parsed.password === "";
  } catch {
    return false;
  }
}

export function validProfileId(value: unknown): value is string {
  return typeof value === "string" && value !== "" && charLength(value) <= MAX_PROFILE_ID_CHARS;
}

/** Keeps string/number/boolean values, drops invalid keys, caps at 30 keys. */
export function sanitizeProps(props: Props | undefined | null): Record<string, PropValue> | undefined {
  if (!props || typeof props !== "object") return undefined;
  const out: Record<string, PropValue> = {};
  let count = 0;
  for (const [key, raw] of Object.entries(props)) {
    if (count >= MAX_PROPS) break;
    if (key === "" || byteLength(key) > MAX_KEY_BYTES) continue;
    let value: PropValue;
    if (typeof raw === "string") value = truncateBytes(raw, MAX_VALUE_BYTES);
    else if (typeof raw === "boolean") value = raw;
    else if (typeof raw === "number" && Number.isFinite(raw)) value = raw;
    else continue;
    out[key] = value;
    count++;
  }
  return count > 0 ? out : undefined;
}

/** Keeps valid string traits (numbers/booleans are stringified), at most 20. */
export function sanitizeTraits(traits: Traits | undefined | null): Record<string, string> | undefined {
  if (!traits || typeof traits !== "object") return undefined;
  const out: Record<string, string> = {};
  let count = 0;
  for (const [key, raw] of Object.entries(traits)) {
    if (count >= MAX_TRAITS) break;
    if (key === "" || charLength(key) > MAX_TRAIT_KEY_CHARS) continue;
    let value: string;
    if (typeof raw === "string") value = raw;
    else if (typeof raw === "boolean" || (typeof raw === "number" && Number.isFinite(raw))) value = String(raw);
    else continue;
    if (charLength(value) > MAX_TRAIT_VALUE_CHARS) continue;
    out[key] = value;
    count++;
  }
  return count > 0 ? out : undefined;
}

const amountPattern = /^\d+(\.\d{1,6})?$/;

/** Returns a revenue object the server will accept, or undefined. */
export function sanitizeRevenue(revenue: Revenue | undefined | null): { amount: string | number; currency?: string } | undefined {
  if (!revenue || typeof revenue !== "object") return undefined;
  let { amount } = revenue;
  const { currency } = revenue;
  if (typeof amount === "number") {
    // Floating point noise (0.1 + 0.2) would exceed the 6-decimal limit.
    amount = Math.round(amount * 1e6) / 1e6;
    if (!Number.isFinite(amount) || amount <= 0 || amount > 1e12) return undefined;
  } else if (typeof amount !== "string" || !amountPattern.test(amount) || Number(amount) <= 0) {
    return undefined;
  }
  if (currency !== undefined && currency !== "" && !/^[A-Z]{3}$/.test(currency)) return undefined;
  return currency ? { amount, currency } : { amount };
}
