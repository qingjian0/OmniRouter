import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
  base: "/",
  server: {
    proxy: {
      "/healthz": "http://127.0.0.1:8080",
      "/readyz": "http://127.0.0.1:8080",
      "/admin": "http://127.0.0.1:8080",
      "/v1": "http://127.0.0.1:8080",
      "/v1beta": "http://127.0.0.1:8080"
    }
  },
  build: {
    outDir: "../internal/admin/static",
    emptyOutDir: true,
    sourcemap: false
  }
});
