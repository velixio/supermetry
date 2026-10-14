"use client";

import { useInitSupermetry, type InitOptions } from "@supermetry/react";

export type { InitOptions, PageviewOptions, Props, Revenue, Tracker, TrackerEvent, TrackOptions, Traits } from "@supermetry/react";
export { getTracker, useSupermetry } from "@supermetry/react";

export type SupermetryProps = Omit<InitOptions, "host" | "src"> & { host?: string; src?: string };

export function Supermetry({ host = "", ...options }: SupermetryProps): null {
  useInitSupermetry({ host, ...options } as InitOptions);
  return null;
}
