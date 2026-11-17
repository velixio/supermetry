import { defineConfig } from "vite";
import { sentryVitePlugin } from "@sentry/vite-plugin";

// SM_VITE_PLUGIN=1 adds the bundler plugin with only url and authToken (what a user sets);
// otherwise the build only produces the map and the CLI does inject and upload.
const plugins = [];
if (process.env.SM_VITE_PLUGIN === "1") {
  plugins.push(sentryVitePlugin({ url: process.env.SM_URL, authToken: process.env.SM_TOKEN, telemetry: false, release: { name: process.env.SM_RELEASE } }));
}
export default defineConfig({
  build: {
    sourcemap: true,
    minify: "esbuild",
    rollupOptions: { input: "src/main.js", output: { entryFileNames: "assets/app.js", chunkFileNames: "assets/[name].js" } },
  },
  plugins,
});
