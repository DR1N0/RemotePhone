export type ControlPlaneEnv = {
  port: number
  workerBaseUrl: string
  databasePath: string
  sessionIdleTtlMs: number
  reconcileIntervalMs: number
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
  return {
    port: parsePort(env.MAKO_CONTROLPLANE_PORT ?? env.PORT, 3000),
    workerBaseUrl: (env.MAKO_WORKER_BASE_URL ?? "http://127.0.0.1:7780").replace(/\/$/, ""),
    databasePath: env.MAKO_CONTROLPLANE_DB_PATH ?? "./data/mako.db",
    sessionIdleTtlMs: parsePositiveInteger(env.MAKO_SESSION_IDLE_TTL_MS, 300_000, "session idle ttl"),
    reconcileIntervalMs: parsePositiveInteger(env.MAKO_RECONCILE_INTERVAL_MS, 5_000, "reconcile interval"),
  }
}
