import { beforeEach, describe, expect, it, vi } from "vitest"

const { loadEnv } = vi.hoisted(() => ({ loadEnv: vi.fn() }))

vi.mock("vite", () => ({ defineConfig: <T>(config: T) => config, loadEnv }))
vi.mock("@vitejs/plugin-react", () => ({ default: () => ({ name: "react" }) }))

import defaultViteConfig, { createAdminViteConfig } from "../vite.config.js"

type ViteConfigCallback = (env: { mode: string }) => ReturnType<typeof createAdminViteConfig>

function evaluateDefaultViteConfig(mode: string) {
  return (defaultViteConfig as ViteConfigCallback)({ mode })
}

describe("Admin Vite development proxy", () => {
  it("proxies /api to the IPv4 loopback management port by default without a legacy session proxy", () => {
    const config = createAdminViteConfig({})

    expect(config.base).toBe("/admin/")
    expect(config.build).toMatchObject({ outDir: "dist", emptyOutDir: true })
    expect(config.server).toMatchObject({
      port: 5173,
      proxy: {
        "/api": { target: "http://127.0.0.1:3001" },
      },
    })
    expect(config.server?.proxy).not.toHaveProperty("/session")
  })

  it("uses a valid MAKO_ADMIN_PORT override for the management proxy", () => {
    const config = createAdminViteConfig({ MAKO_ADMIN_PORT: "4312" })

    expect(config.server).toMatchObject({
      proxy: {
        "/api": { target: "http://127.0.0.1:4312" },
      },
    })
  })

  it.each(["0", "-1", "3001.5", "65536", "not-a-port"])("rejects invalid MAKO_ADMIN_PORT %j", (port) => {
    expect(() => createAdminViteConfig({ MAKO_ADMIN_PORT: port })).toThrow(`Invalid MAKO_ADMIN_PORT: ${port}`)
  })
})

describe("Admin Vite default config callback", () => {
  beforeEach(() => {
    loadEnv.mockReset()
  })

  it("loads the default development environment and proxies to management port 3001", () => {
    loadEnv.mockReturnValue({})

    const config = evaluateDefaultViteConfig("development")

    expect(loadEnv).toHaveBeenCalledTimes(1)
    expect(loadEnv).toHaveBeenCalledWith("development", ".", "")
    expect(config.server).toMatchObject({
      proxy: {
        "/api": { target: "http://127.0.0.1:3001" },
      },
    })
  })

  it("passes a loaded MAKO_ADMIN_PORT override through the default callback", () => {
    loadEnv.mockReturnValue({ MAKO_ADMIN_PORT: "4312" })

    const config = evaluateDefaultViteConfig("test")

    expect(loadEnv).toHaveBeenCalledTimes(1)
    expect(loadEnv).toHaveBeenCalledWith("test", ".", "")
    expect(config.server).toMatchObject({
      proxy: {
        "/api": { target: "http://127.0.0.1:4312" },
      },
    })
  })

  it("rejects an invalid port returned by Vite environment loading", () => {
    loadEnv.mockReturnValue({ MAKO_ADMIN_PORT: "65536" })

    expect(() => evaluateDefaultViteConfig("production")).toThrow("Invalid MAKO_ADMIN_PORT: 65536")
    expect(loadEnv).toHaveBeenCalledTimes(1)
    expect(loadEnv).toHaveBeenCalledWith("production", ".", "")
  })
})
