export type ControlPlaneEnv = {
  /** Public client API port; retained for the existing TLS proxy upstream. */
  port: number
  /** Loopback-only WebUI and administration API port. */
  adminPort: number
  listenHostname: "127.0.0.1"
  workerBaseUrl: string
  databasePath: string
  bootstrapSecretPath: string
  ownerAbsenceTimeoutMs: number
  ownerAbsenceTimeoutMinMs: number
  ownerAbsenceTimeoutMaxMs: number
  reconcileIntervalMs: number
  enableSessionDiagnostic: boolean
}

function parsePort(value: string | undefined, fallback: number): number {
  if (value == null || value.trim() === "") {
    return fallback
  }

  const port = Number(value)
  if (!Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error(`Invalid port value: ${value}`)
  }

  return port
}

function parsePositiveInteger(value: string | undefined, fallback: number, name: string): number {
  if (value == null || value.trim() === "") {
    return fallback
  }

  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`Invalid ${name} value: ${value}`)
  }

  return parsed
}

export function loadEnv(env: NodeJS.ProcessEnv = process.env): ControlPlaneEnv {
  const port = parsePort(env.MAKO_CONTROLPLANE_PORT ?? env.PORT, 3000)
  const adminPort = parsePort(env.MAKO_ADMIN_PORT, 3001)
  if (port === adminPort) {
    throw new Error(`Client and admin ports must differ: ${port}`)
  }

  return {
    port,
    adminPort,
    listenHostname: "127.0.0.1",
    workerBaseUrl: (env.MAKO_WORKER_BASE_URL ?? "http://127.0.0.1:7780").replace(/\/$/, ""),
    databasePath: env.MAKO_CONTROLPLANE_DB_PATH ?? "./data/mako.db",
    bootstrapSecretPath: env.MAKO_BOOTSTRAP_SECRET_PATH ?? `${env.MAKO_CONTROLPLANE_DB_PATH ?? "./data/mako.db"}.bootstrap-secret`,
    ownerAbsenceTimeoutMs: parsePositiveInteger(env.MAKO_OWNER_ABSENCE_TIMEOUT_MS, 300_000, "owner absence timeout"),
    ownerAbsenceTimeoutMinMs: parsePositiveInteger(env.MAKO_OWNER_ABSENCE_TIMEOUT_MIN_MS, 30_000, "owner absence timeout min"),
    ownerAbsenceTimeoutMaxMs: parsePositiveInteger(env.MAKO_OWNER_ABSENCE_TIMEOUT_MAX_MS, 3_600_000, "owner absence timeout max"),
    reconcileIntervalMs: parsePositiveInteger(env.MAKO_RECONCILE_INTERVAL_MS, 5_000, "reconcile interval"),
    enableSessionDiagnostic: env.MAKO_ENABLE_SESSION_DIAGNOSTIC === "true",
  }
}
