import { getTracker, init, type InitOptions, type Tracker } from "@supermetry/tracker";
import { hasInjectionContext, inject, type App, type InjectionKey, type Plugin } from "vue";

export type { InitOptions, PageviewOptions, Props, Revenue, Tracker, TrackerEvent, TrackOptions, Traits } from "@supermetry/tracker";
export { getTracker } from "@supermetry/tracker";

export interface RouterLike {
  afterEach(guard: (to: unknown, from: unknown, failure?: unknown) => unknown): () => void;
}

export type SupermetryOptions = InitOptions & { router?: RouterLike };

export const supermetryKey: InjectionKey<Tracker> = Symbol("supermetry");

export const Supermetry: Plugin<[SupermetryOptions]> = {
  install(app: App, { router, ...options }: SupermetryOptions) {
    const tracker = getTracker();
    app.provide(supermetryKey, tracker);
    app.config.globalProperties.$supermetry = tracker;
    if (typeof window === "undefined") return;
    init({ ...options, manual: router ? true : options.manual } as InitOptions);
    router?.afterEach((_to, _from, failure) => {
      if (!failure) tracker.pageview({ url: window.location.href });
    });
  },
};

export function useSupermetry(): Tracker {
  return (hasInjectionContext() && inject(supermetryKey, null)) || getTracker();
}

declare module "vue" {
  interface ComponentCustomProperties {
    $supermetry: Tracker;
  }
}

export default Supermetry;
