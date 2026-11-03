// DEPRECATED, do not build. This is the old local-browser console. 8325 now serves apps/console-web,
// rebuild it with: cd apps/console-web && pnpm install && pnpm build
throw new Error("packages/browser/frontend is deprecated, build apps/console-web instead (pnpm build there)");

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "./",
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 5177,
    strictPort: true
  },
  build: {
    outDir: "dist",
    emptyOutDir: true
  }
});
