import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Build into ../internal/webserv/dist so Go can go:embed it.
// During dev, proxy /api to the running paladin server.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/webserv/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
});
