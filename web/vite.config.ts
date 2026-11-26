// Vite builds the SPA into web/dist, which Go embeds (web/embed.go). In development Vite serves
// the UI on :5173 and proxies /api to the Go server on :2769 (mise run dev). The proxy keeps the
// browser's Host and Origin (localhost:5173), so crowsnest's host and origin checks pass exactly
// as they will in production, where everything is same-origin.
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const apiTarget = `http://127.0.0.1:${process.env.CROWSNEST_PORT ?? "2769"}`;

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: { "/api": { target: apiTarget, changeOrigin: false, ws: true } },
    // The taglines live in internal/taglines/taglines.txt, shared with the Go binary.
    fs: { allow: [".."] },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsDir: "assets", // fingerprinted; httpd serves these with an immutable cache header
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    restoreMocks: true,
    unstubGlobals: true,
  },
});
