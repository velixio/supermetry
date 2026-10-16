import { Dimensions, Platform } from "react-native";

export const VERSION = "0.2.0";
const SUFFIX = `Supermetry-ReactNative/${VERSION}`;

type PlatformLike = {
  OS: string;
  Version: number | string;
  isPad?: boolean;
  constants?: Record<string, unknown>;
};

function clean(value: unknown): string {
  // Characters that would break the UA comment syntax.
  return typeof value === "string" ? value.replace(/[;()\r\n]/g, " ").replace(/\s+/g, " ").trim() : "";
}

/**
 * Browser-shaped User-Agent so the server's parser detects OS and device class,
 * e.g. `Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Supermetry-ReactNative/0.2.0`.
 */
export function buildUserAgent(platform: PlatformLike = Platform): string {
  const constants = platform.constants ?? {};
  switch (platform.OS) {
    case "ios": {
      const version = (clean(constants.osVersion) || clean(String(platform.Version ?? "")) || "17.0").replace(/\./g, "_");
      return platform.isPad
        ? `Mozilla/5.0 (iPad; CPU OS ${version} like Mac OS X) ${SUFFIX}`
        : `Mozilla/5.0 (iPhone; CPU iPhone OS ${version} like Mac OS X) ${SUFFIX}`;
    }
    case "android": {
      const release = clean(constants.Release) || clean(String(platform.Version ?? ""));
      const model = clean(constants.Model);
      return `Mozilla/5.0 (Linux; Android ${release}${model ? `; ${model}` : ""}) ${SUFFIX}`;
    }
    case "macos":
      return `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) ${SUFFIX}`;
    case "windows":
      return `Mozilla/5.0 (Windows NT 10.0; Win64; x64) ${SUFFIX}`;
    default:
      return `Mozilla/5.0 (${clean(platform.OS) || "Unknown"}) ${SUFFIX}`;
  }
}

export function screenWidth(): number | undefined {
  try {
    const width = Math.round(Dimensions.get("window").width);
    return width > 0 && width <= 32767 ? width : undefined;
  } catch {
    return undefined;
  }
}

/** `"Product Details"` → `"product-details"`, `"Settings/Edit Profile"` → `"settings/edit-profile"`. */
export function screenPath(name: string): string {
  return String(name)
    .split("/")
    .map((segment) =>
      segment
        .normalize("NFKD")
        .replace(/[̀-ͯ]/g, "")
        .replace(/([a-z0-9])([A-Z])/g, "$1-$2")
        .toLowerCase()
        .replace(/[^a-z0-9._~-]+/g, "-")
        .replace(/^-+|-+$/g, ""),
    )
    .filter(Boolean)
    .join("/");
}
