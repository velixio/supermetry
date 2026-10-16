# `@supermetry/vue`

Vue 3 plugin and composable for [Supermetry](https://github.com/velixio/supermetry) analytics, with optional Vue Router integration.

```sh
npm install @supermetry/vue
```

## Quick start

```ts
import { createApp } from "vue";
import { Supermetry } from "@supermetry/vue";
import App from "./App.vue";
import router from "./router";

createApp(App)
  .use(router)
  .use(Supermetry, { site: "your-site-id", router })
  .mount("#app");
```

With `router`, the tracker runs in manual mode and sends a pageview after every successful navigation (`router.afterEach`), including the first one. Without it, `history.pushState` navigations are tracked automatically.

```vue
<script setup lang="ts">
import { useSupermetry } from "@supermetry/vue";

const supermetry = useSupermetry();
const buy = () => supermetry.track("Purchase", { plan: "pro" }, { revenue: { amount: 49.9, currency: "USD" } });
</script>

<template>
  <button @click="buy">Buy</button>
</template>
```

The plugin accepts every [`@supermetry/tracker` option](https://github.com/velixio/supermetry/blob/main/sdk/js/README.md). The tracker is also available as `$supermetry` in templates. `useSupermetry()` returns `pageview`, `track`, `identify`, `consent`, `optIn` and `optOut`; calls are queued until the script loads, and the plugin does nothing during server rendering, so it is safe in Nuxt and other SSR setups.
