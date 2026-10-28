import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/skillhub.": { target: "http://127.0.0.1:8080" },
      "/healthz": { target: "http://127.0.0.1:8080" },
      "/auth/oidc/": { target: "http://127.0.0.1:8080" },
    },
  },
})
