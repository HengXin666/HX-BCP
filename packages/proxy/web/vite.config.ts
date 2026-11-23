import path from "node:path"

import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, loadEnv } from "vite"

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "")
  const backendTarget = env.VITE_BACKEND_TARGET || "http://127.0.0.1:19090"

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        "@": path.resolve(process.cwd(), "src"),
      },
    },
    build: {
      // Vite 8 builds with rolldown, which replaces Rollup's `manualChunks`
      // with `output.codeSplitting.groups`. Pinning the three shared vendor
      // families into their own chunks keeps the eager entry from carrying
      // them and lets a page chunk reuse a still-cached vendor chunk instead
      // of re-downloading shared deps.
      rolldownOptions: {
        output: {
          codeSplitting: {
            groups: [
              // React core + its scheduler. Highest priority so nothing else
              // can claim these modules.
              { name: "vendor-react", test: /node_modules[\\/](react|react-dom|scheduler)[\\/]/, priority: 30 },
              // Radix primitives and their floating-ui / aria helper deps.
              { name: "vendor-radix", test: /node_modules[\\/](@radix-ui|@floating-ui|aria-hidden|react-remove-scroll|react-remove-scroll-bar|react-style-singleton|use-callback-ref|use-sidecar|get-nonce|detect-node-es)[\\/]/, priority: 20 },
              // Icon set: large and shared by nearly every page, so it is a
              // dedicated cacheable chunk.
              { name: "vendor-icons", test: /node_modules[\\/]lucide-react[\\/]/, priority: 20 },
              // Everything else from node_modules (clsx, tailwind-merge,
              // class-variance-authority, ...) in one long-lived vendor chunk.
              // xterm is excluded on purpose: it must stay reachable only from
              // the lazily imported terminal page, otherwise this chunk becomes
              // an eager dependency of the entry and is preloaded on first
              // paint for every visit.
              {
                name: "vendor",
                test: (id: string) => id.includes("node_modules") && !id.includes("xterm"),
                priority: 1,
              },
            ],
          },
        },
      },
    },
    server: {
      strictPort: true,
      proxy: {
        "/api": {
          target: backendTarget,
          changeOrigin: false,
          // Required for the v2 in-browser terminal (/api/v1/terminal/ws).
          ws: true,
        },
        "/health": {
          target: backendTarget,
          changeOrigin: false,
        },
        // Public client subscriptions are served outside the authenticated
        // management API. Proxy them in development so copied localhost URLs
        // return the subscription document instead of Vite's SPA fallback.
        "/sub": {
          target: backendTarget,
          changeOrigin: false,
        },
        // Public residential rotate endpoints (/rot/<token>) are token
        // addressed and sit outside the authenticated API, same as /sub.
        "/rot": {
          target: backendTarget,
          changeOrigin: false,
        },
        // Programmatic node listing (GET /nodes/<share-token>) is the same
        // class of public, token-addressed contract as /sub. Without this
        // proxy Vite returns the SPA HTML instead of the JSON listing, so a
        // program testing the contract against the dev origin reads garbage.
        "/nodes": {
          target: backendTarget,
          changeOrigin: false,
        },
      },
    },
  }
})
