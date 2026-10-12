# `@supermetry/react-native`

React Native SDK for [Supermetry](../SPEC.md): screen views, custom events, identify, persisted opt-out, and an offline-tolerant queue.

## Install

```sh
npm install @supermetry/react-native @react-native-async-storage/async-storage
```

`@react-native-async-storage/async-storage` is optional; without a `storage`, the profile ID, opt-out and undelivered events live in memory only.

## Quick start

```tsx
import AsyncStorage from "@react-native-async-storage/async-storage";
import { NavigationContainer, useNavigationContainerRef } from "@react-navigation/native";
import { SupermetryProvider, createSupermetry, trackNavigation, useSupermetry } from "@supermetry/react-native";

const supermetry = createSupermetry({
  site: "your-site-id",
  host: "https://analytics.example.com",
  appUrl: "https://app.example.com", // must be an allowed host of the site
  storage: AsyncStorage,
});

export default function App() {
  const navigationRef = useNavigationContainerRef();
  return (
    <SupermetryProvider client={supermetry}>
      <NavigationContainer ref={navigationRef} {...trackNavigation(supermetry, navigationRef)}>
        {/* screens */}
      </NavigationContainer>
    </SupermetryProvider>
  );
}

function CheckoutButton() {
  const supermetry = useSupermetry();
  return (
    <Button
      title="Buy"
      onPress={() => supermetry.track("Purchase", { sku: "A1" }, { revenue: { amount: "9.99", currency: "USD" } })}
    />
  );
}
```

## API

- `createSupermetry(options)` → client. Options: `site`, `host`, `appUrl` (required), `endpoint`, `storage`, `disabled`, `onError`, `fetch`, `userAgent`, `maxQueueSize` (500), `maxAttempts` (10), `retryDelay` (1000 ms), `maxRetryDelay` (60000 ms), `storagePrefix` (`"supermetry:"`).
- `screen(name, props?)` sends a `pageview` for `appUrl + "/" + slug(name)` (`"Product Details"` → `/product-details`, `"Settings/Edit Profile"` → `/settings/edit-profile`) and makes it the current screen.
- `pageview(urlOrPath?, props?)` sends a pageview for an absolute URL or a path under `appUrl`.
- `track(name, props?, { revenue?, url? })`: custom event, URL defaults to the current screen.
- `identify(profileId, traits?)` persists the profile ID and attaches it to every later event; `identify(null)` / `reset()` clears it.
- `optOut()` / `optIn()` (persisted), `isOptedOut()`, `profileId`, `ready` (storage loaded), `flush()`, `destroy()`.
- `<SupermetryProvider client>` and `useSupermetry()`.
- `trackNavigation(client, navigationRef, { screenName?, props? })` returns `{ onReady, onStateChange }` for React Navigation's `<NavigationContainer>`.

## Behaviour

- **User-Agent**: a browser-shaped UA built from `Platform` so the server detects OS and device, e.g. `Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Supermetry-ReactNative/0.1.0` or `Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-ReactNative/0.1.0`.
- `screen_width` comes from `Dimensions.get("window").width`.
- **Delivery**: events go to an in-memory queue and are sent in order. Network errors, 429 and 5xx are retried with exponential backoff (honouring `Retry-After`) and the same `id`/`ts`, so the server deduplicates; `400` drops the event. When the app goes to the background the queue is flushed and anything undelivered is written to `storage`, then restored on the next launch. Events older than 24 h are discarded.
- Props are capped at 30 string/number/boolean values; invalid traits are dropped locally; traits are only ever sent on `identify`.
- Tracking never throws; problems go to `onError`.

License: Apache-2.0
