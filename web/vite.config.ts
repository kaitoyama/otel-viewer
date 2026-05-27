import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "node:path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { "@": resolve(__dirname, "src") },
  },
  build: {
    // Emit straight into the Go embed dir so `go build` picks it up.
    outDir: resolve(__dirname, "..", "internal", "api", "dist"),
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
      "/v1": "http://localhost:8080",
    },
  },
  test: {
    environment: "jsdom",
  },
});
