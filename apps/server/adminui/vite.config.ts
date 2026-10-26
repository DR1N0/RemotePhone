import { defineConfig, loadEnv } from "vite"
import react from "@vitejs/plugin-react"

function parseAdminPort(value: string | undefined): number {
  if (value == null || value.trim() === "") {
    return 3001
  }

  const port = Number(value)
  if (!Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error(`Invalid MAKO_ADMIN_PORT: ${value}`)
  }

  return port
}

export function createAdminViteConfig(env: Record<string, string | undefined>) {
  const adminPort = parseAdminPort(env.MAKO_ADMIN_PORT)

  return {
    plugins: [react()],
    base: "/admin/",
    server: {
      port: 5173,
      proxy: {
        "/api": { target: `http://127.0.0.1:${adminPort}` },
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
    },
  }
}

export default defineConfig(({ mode }) => createAdminViteConfig(loadEnv(mode, ".", "")))
