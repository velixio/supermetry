import { defineConfig } from "vite";
import { sentryVitePlugin } from "@sentry/vite-plugin";

// Debug ids are injected by the plugin; upload is disabled (no network, no token).
export default defineConfig({
  build: {
    sourcemap: true,
    minify: "esbuild",
    rollupOptions: { output: { entryFileNames: "assets/app.js", chunkFileNames: "assets/[name].js" } },
  },
  plugins: [
    sentryVitePlugin({
      telemetry: false,
      release: { name: "fixture-1.0.0", inject: false, create: false, finalize: false },
      sourcemaps: { disable: false },
      // Upload is skipped because no org/project/authToken are configured.
      silent: true,
      errorHandler: () => {},
    }),
  ],
});
