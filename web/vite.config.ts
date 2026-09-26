import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev: `npm run dev` proxies API and public pages to a backend on :8080.
const backend = process.env.FERRY_BACKEND ?? "http://localhost:8080";
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: Object.fromEntries(["/api", "/s/", "/u/", "/_ferry", "/healthz", "/readyz"].map((p) => [p, { target: backend, changeOrigin: false }])),
  },
  build: { outDir: "dist", sourcemap: false, chunkSizeWarningLimit: 800 },
});
