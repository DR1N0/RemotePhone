export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message)
    this.name = "ApiError"
  }
}

export type User = {
  id: string
  username: string | null
  displayName: string
  status: "active" | "banned"
  type: "guest" | "registered" | "admin" | "system"
}

export type AdminCredential = {
  id: string
  deviceLabel: string
  scope: "account"
  notBefore: string
  expiresAt: string | null
  revokedAt: string | null
  createdAt: string
  updatedAt: string
}

export type AdminInstance = {
  id: string
  ownerId: string
  name: string
  runtimeClass: string
  templateId: string
  desiredState: string
  observedState: string
  lastError: string | null
  createdAt: string
  updatedAt: string
}

export type AuditEvent = {
  id: string
  actorUserId: string | null
  actorCredentialId: string | null
  action: string
  targetType: string
  targetId: string
  requestId: string
  outcome: "attempt" | "success" | "failure"
  createdAt: string
}

export type AccessCode = {
  code: string
  expiresAt: string
  target: string
}

export type ServerIdentity = {
  serverId: string
  serverName: string
  certSha256: string
}

export type SetupAdminRequest = {
  username: string
  password: string
  deviceLabel: string
  bootstrapSecret: string
}

type Page<T> = { items: T[]; nextCursor: string | null }

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = {
    "content-type": "application/json",
    ...(init?.headers as Record<string, string> | undefined),
  }
  const response = await fetch(path, { ...init, headers })
  if (response.status === 204) return undefined as T
  if (!response.ok) {
    const body = await response.text()
    let message = body || `HTTP ${response.status}`
    try {
      const parsed = JSON.parse(body) as { error?: unknown }
      if (typeof parsed.error === "string") message = parsed.error
    } catch {
      // Non-JSON errors are displayed as supplied.
    }
    throw new ApiError(response.status, message)
  }
  return await response.json() as T
}

function pageQuery({ limit, cursor }: { limit: number; cursor: string | null }): string {
  const params = new URLSearchParams({ limit: String(limit) })
  if (cursor != null) params.set("cursor", cursor)
  return params.toString()
}

export const api = {
  getAuthStatus: () => request<{ claimed: boolean }>("/api/v1/auth/status"),
  setupAdmin: (body: SetupAdminRequest) =>
    request<{ user: User; token: string }>("/api/v1/auth/setup", { method: "POST", body: JSON.stringify(body) }),

  listUsers: (input: { limit: number; cursor: string | null }) =>
    request<Page<User>>(`/api/v1/admin/users?${pageQuery(input)}`),
  setUserType: (id: string, type: "registered" | "admin") =>
    request<void>(`/api/v1/admin/users/${encodeURIComponent(id)}/type`, { method: "POST", body: JSON.stringify({ type }) }),
  setBanStatus: (id: string, status: "active" | "banned") =>
    request<void>(`/api/v1/admin/users/${encodeURIComponent(id)}/${status === "banned" ? "ban" : "unban"}`, { method: "POST" }),
  listUserCredentials: (id: string) =>
    request<{ items: AdminCredential[] }>(`/api/v1/admin/users/${encodeURIComponent(id)}/credentials`).then((result) => result.items),
  revokeUserCredential: (userId: string, credentialId: string) =>
    request<void>(`/api/v1/admin/users/${encodeURIComponent(userId)}/credentials/${encodeURIComponent(credentialId)}/revoke`, { method: "POST" }),
  createAccessCode: (input: { username?: string; ttlSeconds?: number } = {}) =>
    request<AccessCode>("/api/v1/admin/access-codes", { method: "POST", body: JSON.stringify(input) }),
  resetPassword: (input: { username: string; password: string }) =>
    request<void>("/api/v1/admin/users/reset-password", { method: "POST", body: JSON.stringify(input) }),

  getServer: () => request<ServerIdentity>("/api/v1/admin/server"),
  listAdminInstances: () =>
    request<{ items: AdminInstance[] }>("/api/v1/admin/instances").then((result) => result.items),
  startInstance: (id: string) => request<{ instance: AdminInstance }>(`/api/v1/admin/instances/${encodeURIComponent(id)}/start`, { method: "POST" }).then((result) => result.instance),
  stopInstance: (id: string) => request<{ instance: AdminInstance }>(`/api/v1/admin/instances/${encodeURIComponent(id)}/stop`, { method: "POST" }).then((result) => result.instance),
  deleteInstance: (id: string) => request<void>(`/api/v1/admin/instances/${encodeURIComponent(id)}`, { method: "DELETE" }),
  listAudit: (input: { limit: number; cursor: string | null }) =>
    request<Page<AuditEvent>>(`/api/v1/admin/audit?${pageQuery(input)}`),
}
