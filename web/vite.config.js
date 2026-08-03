import { defineConfig } from "vite";

export default defineConfig({
  server: { proxy: { "/api": process.env.VITE_CONTROLLER_URL || "http://127.0.0.1:8080" } },
});
