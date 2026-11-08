import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: { "/api": process.env.TREMOLO_API ?? "http://localhost:8080" },
  },
  build: { outDir: "dist", emptyOutDir: true },
});
