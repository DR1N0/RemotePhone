import { describe, expect, it } from "vitest"

import { loadEnv } from "../src/config/env.js"

describe("control-plane env", () => {
  it("loads the control-plane port, worker base url, database path, and reconciliation settings from env", () => {
    const env = loadEnv({
      MAKO_CONTROLPLANE_PORT: "4010",
      MAKO_WORKER_BASE_URL: "http://127.0.0.1:7788/",
      MAKO_CONTROLPLANE_DB_PATH: "/tmp/mako.db",
      MAKO_SESSION_IDLE_TTL_MS: "120000",
      MAKO_RECONCILE_INTERVAL_MS: "7000",
    })

    expect(env).toEqual({
      port: 4010,
      workerBaseUrl: "http://127.0.0.1:7788",
      databasePath: "/tmp/mako.db",
      sessionIdleTtlMs: 120000,
      reconcileIntervalMs: 7000,
    })
  })

  it("falls back to the default local worker url, port, sqlite path, and reconciliation settings", () => {
    const env = loadEnv({})

    expect(env).toEqual({
      port: 3000,
      workerBaseUrl: "http://127.0.0.1:7780",
      databasePath: "./data/mako.db",
      sessionIdleTtlMs: 300000,
      reconcileIntervalMs: 5000,
    })
  })
})
