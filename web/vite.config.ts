import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// During dev, proxy API + agent/webhook/event paths to the Go control plane
// so the SPA and API share an origin (cookies + SSE just work).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: true },
      "/webhooks": { target: "http://localhost:8080", changeOrigin: true },
    },
  },
});
