export interface Frame {
  path: string;
  line: number;
  fn: string;
}

const MAX_FRAMES_BYTES = 4096;

const enc = new TextEncoder();

/** Default first-party filter: drops dependencies, runtime internals and frozen modules. */
export function defaultInApp(path: string): boolean {
  const p = path.replace(/\\/g, "/");
  return !(p.includes("node_modules") || p.includes("site-packages") || p.startsWith("<frozen") || p.startsWith("node:"));
}

/**
 * Parses a V8 stack into frames. The first lines of a V8 stack hold the error
 * message, so only "at ..." lines are read, and the message text is stripped first.
 */
export function parseV8Stack(stack: string, message = ""): Frame[] {
  let text = stack;
  if (message) {
    const i = text.indexOf(message);
    if (i >= 0) text = text.slice(i + message.length);
  }
  const frames: Frame[] = [];
  for (const raw of text.split("\n")) {
    const m = /^\s*at\s+(.*)$/.exec(raw);
    if (!m) continue;
    const body = m[1]!.trim();
    let fn = "<anonymous>";
    let loc = body;
    const paren = /^(.*?)\s+\((.*)\)$/.exec(body);
    if (paren) {
      fn = paren[1]!;
      loc = paren[2]!;
    }
    const lm = /^(.*?):(\d+)(?::\d+)?$/.exec(loc);
    if (!lm) continue;
    frames.push({ path: lm[1]!, line: Number(lm[2]), fn });
  }
  return frames;
}

const HOME_RE = /^(?:\/Users\/[^/]+|\/home\/[^/]+|[a-z]:\/Users\/[^/]+)(?:\/|$)/i;

/** Scrubs one path: home folders become `~/`, other absolute paths keep their last 3 segments. */
export function scrubPath(path: string): string {
  let p = path.replace(/^file:\/\//i, "").replace(/\\/g, "/");
  if (/^\/[a-z]:\//i.test(p)) p = p.slice(1);
  const home = HOME_RE.exec(p);
  if (home) return "~/" + p.slice(home[0].length);
  const absolute = p.startsWith("/") || /^[a-z]:\//i.test(p);
  if (!absolute) return p;
  const segs = p.replace(/^[a-z]:/i, "").split("/").filter(Boolean);
  return segs.slice(-3).join("/");
}

/** Scrubs newline-separated `path:line function` lines and caps the result on a line boundary. */
export function scrubFrames(text: string, maxBytes = MAX_FRAMES_BYTES): string {
  const out: string[] = [];
  let size = 0;
  for (const raw of text.split("\n")) {
    if (!raw.trim()) continue;
    const m = /^(.*?):(\d+)(.*)$/.exec(raw);
    const line = m ? scrubPath(m[1]!) + ":" + m[2] + m[3] : scrubPath(raw);
    const add = enc.encode(line).length + (out.length ? 1 : 0);
    if (size + add > maxBytes) break;
    out.push(line);
    size += add;
  }
  return out.join("\n");
}

/** Filters to first-party frames, scrubs and caps. */
export function buildFrames(frames: Frame[], inApp: (path: string) => boolean = defaultInApp, maxBytes = MAX_FRAMES_BYTES): string {
  const lines = frames
    .filter((f) => inApp(f.path.replace(/^file:\/\//i, "")))
    .map((f) => `${f.path}:${f.line} ${f.fn}`);
  return scrubFrames(lines.join("\n"), maxBytes);
}
