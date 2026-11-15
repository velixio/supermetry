export interface PlatformInfo {
  platform: string;
  arch: string;
  osVersion?: string;
  runtime: string;
}

function proc(): any {
  return (globalThis as any).process;
}

function majorMinor(v: string | undefined): string | undefined {
  const m = /^(\d+)(?:\.(\d+))?/.exec(v ?? "");
  return m ? (m[2] !== undefined ? `${m[1]}.${m[2]}` : m[1]) : undefined;
}

/** Best-effort platform facts; every field degrades to "other" or undefined, never throws. */
export function detectPlatform(): PlatformInfo {
  const p = proc();
  const g = globalThis as any;
  if (!p?.versions) {
    return { platform: "web", arch: "other", runtime: g.Deno ? "deno" : "browser", osVersion: undefined};
  }
  const platform = ({ win32: "windows", darwin: "macos", linux: "linux", android: "android" } as Record<string, string>)[p.platform] ?? "other";
  const arch = ({ x64: "x64", arm64: "arm64", ia32: "x86", arm: "arm" } as Record<string, string>)[p.arch] ?? "other";
  const runtime = p.versions.electron ? "electron" : p.versions.bun ? "bun" : g.Deno ? "deno" : "node";
  let osVersion: string | undefined;
  try {
    if (typeof p.getSystemVersion === "function") osVersion = majorMinor(p.getSystemVersion());
    else if (platform !== "macos") osVersion = majorMinor(p.getBuiltinModule?.("os")?.release?.());
  } catch {
    /* best effort */
  }
  return { platform, arch, osVersion, runtime };
}
