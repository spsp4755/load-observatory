import { defineConfig } from "vite";

export default defineConfig({
  server: { proxy: { "/api": process.env.VITE_CONTROLLER_URL || "http://localhost:8080" } },
});
