import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/source": { target: "ws://localhost:3000", ws: true },
      "/destination": { target: "ws://localhost:3000", ws: true },
    },
  },
});
