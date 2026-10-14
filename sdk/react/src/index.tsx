"use client";

import { getTracker, init, type InitOptions, type Tracker } from "@supermetry/tracker";
import { useEffect, type ReactNode } from "react";

export type { InitOptions, PageviewOptions, Props, Revenue, Tracker, TrackerEvent, TrackOptions, Traits } from "@supermetry/tracker";
export { getTracker } from "@supermetry/tracker";

export type SupermetryProviderProps = InitOptions & { children?: ReactNode };

export function useInitSupermetry(options: InitOptions): Tracker {
  useEffect(() => {
    init(options);
  }, []);
  return getTracker();
}

export function SupermetryProvider({ children, ...options }: SupermetryProviderProps) {
  useInitSupermetry(options as InitOptions);
  return <>{children}</>;
}

export function useSupermetry(): Tracker {
  return getTracker();
}
