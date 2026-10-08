import { ADMIN_AUTHORIZATION_LOST_EVENT, adminSession } from "../auth/session.js"

const BASE = ""

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
  type: "guest" | "registered" | "admin"
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

export type SessionAttachment = {
  id: string
  sessionId: string
  clientId: string
  role: "owner" | "participant" | "observer"
  status: "connected" | "disconnected"
}

export type Session = {
  id: string
  instanceId: string
  status: "active" | "idle" | "closed"
  attachments: SessionAttachment[]
}

export type SetupAdminRequest = {
  username: string
  password: string
  deviceLabel: string
  bootstrapSecret: string
}

export type LoginRequest = {
  username: string
  password: string
  deviceLabel: string
}

type AuthResponse = { user: User; token: string }
type Page<T> = { items: T[]; nextCursor: string | null }

async function request<T>(
  path: string,
  init?: RequestInit,
  bearerToken = adminSession.getToken(),
  adminAuthorizationRequired = false,
): Promise<T> {
  const headers = {
    "content-type": "application/json",
    ...(init?.headers as Record<string, string> | undefined),
    ...(bearerToken == null ? {} : { Authorization: `Bearer ${bearerToken}` }),
  }
  const response = await fetch(`${BASE}${path}`, { ...init, headers })

  if (response.status === 401 && bearerToken === adminSession.getToken()) {
    adminSession.clear()
  }
  if (response.status === 403 && adminAuthorizationRequired && bearerToken === adminSession.getToken()) {
    window.dispatchEvent(new Event(ADMIN_AUTHORIZATION_LOST_EVENT))
  }
  if (response.status === 204) return undefined as T
  if (!response.ok) {
    const body = await response.text()
    let message = body || `HTTP ${response.status}`
    try {
      const parsed = JSON.parse(body) as { error?: unknown }
      if (typeof parsed.error === "string") message = parsed.error
    } catch {
      // Non-JSON errors are intentionally displayed as supplied by the server.
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
  setupAdmin: (body: SetupAdminRequest) => request<AuthResponse>("/api/v1/auth/setup", { method: "POST", body: JSON.stringify(body) }),
  login: (body: LoginRequest) => request<AuthResponse>("/api/v1/auth/login", { method: "POST", body: JSON.stringify(body) }),
  getMe: () => request<{ user: User }>("/api/v1/auth/me"),
  logout: () => request<void>("/api/v1/auth/logout", { method: "POST" }),

  listUsers: (input: { limit: number; cursor: string | null }) =>
    request<Page<User>>(`/api/v1/admin/users?${pageQuery(input)}`, undefined, undefined, true),
  setUserType: (id: string, type: "registered" | "admin") =>
    request<void>(`/api/v1/admin/users/${encodeURIComponent(id)}/type`, { method: "POST", body: JSON.stringify({ type }) }, undefined, true),
  setBanStatus: (id: string, status: "active" | "banned") =>
    request<void>(`/api/v1/admin/users/${encodeURIComponent(id)}/${status === "banned" ? "ban" : "unban"}`, { method: "POST" }, undefined, true),
  listUserCredentials: (id: string) =>
    request<{ items: AdminCredential[] }>(`/api/v1/admin/users/${encodeURIComponent(id)}/credentials`, undefined, undefined, true).then((result) => result.items),
  revokeUserCredential: (userId: string, credentialId: string) =>
    request<void>(`/api/v1/admin/users/${encodeURIComponent(userId)}/credentials/${encodeURIComponent(credentialId)}/revoke`, { method: "POST" }, undefined, true),

  createRegistrationInvite: () =>
    request<{ token: string; expiresAt: string }>("/api/v1/auth/registration-invites", { method: "POST", body: "{}" }, undefined, true),

  listAdminInstances: () =>
    request<{ items: AdminInstance[] }>("/api/v1/admin/instances", undefined, undefined, true).then((result) => result.items),
  startInstance: (id: string) => request<{ instance: AdminInstance }>(`/api/v1/instances/${encodeURIComponent(id)}/start`, { method: "POST" }, undefined, true).then((result) => result.instance),
  stopInstance: (id: string) => request<{ instance: AdminInstance }>(`/api/v1/instances/${encodeURIComponent(id)}/stop`, { method: "POST" }, undefined, true).then((result) => result.instance),
  deleteInstance: (id: string) => request<void>(`/api/v1/instances/${encodeURIComponent(id)}`, { method: "DELETE" }, undefined, true),

  listAudit: (input: { limit: number; cursor: string | null }) =>
    request<Page<AuditEvent>>(`/api/v1/admin/audit?${pageQuery(input)}`, undefined, undefined, true),

  // The development diagnostic deliberately accepts an attachment bearer explicitly.
  // It never falls back to the WebUI administrator credential.
  getDiagnosticSession: (sessionId: string, attachmentToken: string) =>
    request<{ session: Session }>(`/api/v1/sessions/${encodeURIComponent(sessionId)}`, undefined, attachmentToken).then((result) => result.session),
  sendDiagnosticOffer: (sessionId: string, attachmentId: string, sdpOffer: string, attachmentToken: string) =>
    request<{ sdpAnswer: string }>("/api/v1/signaling/session", {
      method: "POST",
      body: JSON.stringify({ sessionId, attachmentId, sdpOffer }),
    }, attachmentToken),
  sendDiagnosticICECandidate: (sessionId: string, attachmentId: string, candidate: string, attachmentToken: string) =>
    request<void>("/api/v1/signaling/ice-candidate", {
      method: "POST",
      body: JSON.stringify({ sessionId, attachmentId, candidate }),
    }, attachmentToken),
  disconnectDiagnosticAttachment: (
    sessionId: string,
    attachmentId: string,
    attachmentToken: string,
    init?: Pick<RequestInit, "keepalive">,
  ) => request<void>(`/api/v1/sessions/${encodeURIComponent(sessionId)}/attachments/${encodeURIComponent(attachmentId)}/disconnect`, {
    method: "POST",
    ...init,
  }, attachmentToken),
}
