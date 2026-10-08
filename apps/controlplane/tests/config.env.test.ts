import { describe, expect, it } from "vitest"

import { loadEnv } from "../src/config/env.js"

describe("control-plane env", () => {
  it("loads the control-plane port, worker base url, database path, owner-absence timeout bounds, and reconciliation settings from env", () => {
    const env = loadEnv({
      MAKO_CONTROLPLANE_PORT: "4010",
      MAKO_ADMIN_PORT: "4011",
      MAKO_WORKER_BASE_URL: "http://127.0.0.1:7788/",
      MAKO_CONTROLPLANE_DB_PATH: "/tmp/mako.db",
      MAKO_BOOTSTRAP_SECRET_PATH: "/tmp/mako.bootstrap-secret",
      MAKO_OWNER_ABSENCE_TIMEOUT_MS: "120000",
      MAKO_OWNER_ABSENCE_TIMEOUT_MIN_MS: "15000",
      MAKO_OWNER_ABSENCE_TIMEOUT_MAX_MS: "1800000",
      MAKO_RECONCILE_INTERVAL_MS: "7000",
      MAKO_ENABLE_SESSION_DIAGNOSTIC: "true",
    })

    expect(env).toEqual({
      port: 4010,
      adminPort: 4011,
      listenHostname: "127.0.0.1",
      workerBaseUrl: "http://127.0.0.1:7788",
      databasePath: "/tmp/mako.db",
      bootstrapSecretPath: "/tmp/mako.bootstrap-secret",
      ownerAbsenceTimeoutMs: 120000,
      ownerAbsenceTimeoutMinMs: 15000,
      ownerAbsenceTimeoutMaxMs: 1800000,
      reconcileIntervalMs: 7000,
      enableSessionDiagnostic: true,
    })
  })

  it("falls back to the default local worker url, port, sqlite path, owner-absence timeout bounds, and reconciliation settings", () => {
    const env = loadEnv({})

    expect(env).toEqual({
      port: 3000,
      adminPort: 3001,
      listenHostname: "127.0.0.1",
      workerBaseUrl: "http://127.0.0.1:7780",
      databasePath: "./data/mako.db",
      bootstrapSecretPath: "./data/mako.db.bootstrap-secret",
      ownerAbsenceTimeoutMs: 300000,
      ownerAbsenceTimeoutMinMs: 30000,
      ownerAbsenceTimeoutMaxMs: 3600000,
      reconcileIntervalMs: 5000,
      enableSessionDiagnostic: false,
    })
  })

  it("rejects malformed, out-of-range, and equal listener ports", () => {
    expect(() => loadEnv({ MAKO_CONTROLPLANE_PORT: "nope" })).toThrow("Invalid port")
    expect(() => loadEnv({ MAKO_ADMIN_PORT: "65536" })).toThrow("Invalid port")
    expect(() => loadEnv({ MAKO_CONTROLPLANE_PORT: "4100", MAKO_ADMIN_PORT: "4100" })).toThrow("must differ")
  })
})
