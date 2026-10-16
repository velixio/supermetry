# `@supermetry/react`

React bindings for [Supermetry](https://github.com/velixio/supermetry) analytics: a provider that loads the tracker and a hook that returns it.

```sh
npm install @supermetry/react
```

## Quick start

```tsx
import { SupermetryProvider, useSupermetry } from "@supermetry/react";

export function App() {
  return (
    <SupermetryProvider site="your-site-id">
      <Checkout />
    </SupermetryProvider>
  );
}

function Checkout() {
  const supermetry = useSupermetry();
  return (
    <button
      onClick={() =>
        supermetry.track("Purchase", { plan: "pro", seats: 3 }, { revenue: { amount: "49.90", currency: "USD" } })
      }
    >
      Buy
    </button>
  );
}
```

`SupermetryProvider` accepts every [`@supermetry/tracker` option](https://github.com/velixio/supermetry/blob/main/sdk/js/README.md) (`site`, optional `host` (defaults to Supermetry Cloud) or `src`, `manual`, `hash`, `consent`, `beforeSend`, …) and injects the script once, after mount.

`useSupermetry()` returns the tracker: `pageview`, `track`, `identify`, `consent`, `optIn`, `optOut`. It works anywhere, with or without the provider in the tree; calls made before the script loads are queued, and during server rendering every method is a no-op.

```tsx
useEffect(() => {
  if (user) supermetry.identify(user.id, { plan: user.plan });
}, [user]);
```

Client-side navigations through `history.pushState` (React Router, TanStack Router, …) are tracked automatically. For route-aware manual tracking use `@supermetry/remix`, or pass `manual` and call `pageview()` yourself.
