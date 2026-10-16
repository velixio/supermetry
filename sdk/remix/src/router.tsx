import { getTracker, useInitSupermetry, type InitOptions } from "@supermetry/react";
import { useEffect } from "react";

export type { InitOptions, PageviewOptions, Props, Revenue, Tracker, TrackerEvent, TrackOptions, Traits } from "@supermetry/react";
export { getTracker, useSupermetry } from "@supermetry/react";

export interface RouteLocation {
  pathname: string;
  search: string;
  hash: string;
}

export type SupermetryProps = Omit<InitOptions, "host" | "src" | "manual"> & { host?: string; src?: string };

export function usePageviews(location: RouteLocation, hash = false): void {
  const key = location.pathname + location.search + (hash ? location.hash : "");
  useEffect(() => {
    getTracker().pageview({ url: window.location.href });
  }, [key]);
}

export function useSupermetryPageviews(location: RouteLocation, options: SupermetryProps): void {
  useInitSupermetry({ ...options, manual: true } as InitOptions);
  usePageviews(location, options.hash);
}
