"use client";

import { useInitSupermetry, type InitOptions } from "@supermetry/react";

export type { InitOptions, PageviewOptions, Props, Revenue, Tracker, TrackerEvent, TrackOptions, Traits } from "@supermetry/react";
export { getTracker, useSupermetry } from "@supermetry/react";

export type SupermetryProps = Omit<InitOptions, "host" | "src"> & { host?: string; src?: string };

export function Supermetry(options: SupermetryProps): null {
  useInitSupermetry(options as InitOptions);
  return null;
}
