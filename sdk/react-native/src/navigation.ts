import type { SupermetryClient } from "./client";
import type { Props } from "./validate";

/** The subset of a React Navigation container ref this helper needs. */
export interface NavigationRefLike {
  getCurrentRoute(): { key?: string; name: string; params?: object } | undefined;
}

export type NavigationRoute = NonNullable<ReturnType<NavigationRefLike["getCurrentRoute"]>>;

export interface TrackNavigationOptions {
  /** Screen name to send; return `undefined` to skip a route. Defaults to `route.name`. */
  screenName?: (route: NavigationRoute) => string | undefined;
  /** Extra props for the screen pageview. */
  props?: (route: NavigationRoute) => Props | undefined;
}

const lastRoute = new WeakMap<object, string>();

/**
 * Returns `{ onReady, onStateChange }` to spread onto React Navigation's
 * `<NavigationContainer>`; each focused route change sends `client.screen(route.name)`.
 */
export function trackNavigation(
  client: SupermetryClient,
  navigationRef: NavigationRefLike,
  options: TrackNavigationOptions = {},
): { onReady: () => void; onStateChange: () => void } {
  const handle = () => {
    try {
      const route = navigationRef.getCurrentRoute();
      if (!route) return;
      const id = route.key ?? route.name;
      if (lastRoute.get(navigationRef) === id) return;
      lastRoute.set(navigationRef, id);
      const name = options.screenName ? options.screenName(route) : route.name;
      if (name) client.screen(name, options.props?.(route));
    } catch {
      // Never break navigation.
    }
  };
  return { onReady: handle, onStateChange: handle };
}
