// Base URL: empty string — all requests go to the same origin (Vite proxies /api in dev)
const BASE = ""

export type Instance = {
  id: string
  name: string
  runtimeClass: string
  templateId: string
  desiredState: string
  observedState: string
  runtimeHandle: {
    pid?: number
    adbPort?: number
    workerBaseUrl?: string
  } | null
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

export type Template = {
  templateId: string
  runtimeClass: string
  displayName: string
}

export type CreateInstanceRequest = {
  name: string
  templateId: string
  placement: { kind: "local-only" }
  resourceProfile: {
    cpuMillicores: number
    memoryMb: number
    diskMb: number
    accelerationMode: "forbid" | "prefer" | "require" | "auto"
    streamProfile: "prototype" | "balanced" | "high"
  }
  launchConfig: {
    headless: boolean
    readOnly: boolean
    wipeData: boolean
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { "content-type": "application/json", ...init?.headers },
    ...init,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText })) as { error?: string }
    throw new Error(err.error ?? `HTTP ${res.status}`)
  }
  return res.json() as Promise<T>
}

export const api = {
  listTemplates: () =>
    request<{ templates: Template[] }>("/api/v1/templates").then((r) => r.templates),

  createInstance: (body: CreateInstanceRequest) =>
    request<{ instance: Instance }>("/api/v1/instances", {
      method: "POST",
      body: JSON.stringify(body),
    }).then((r) => r.instance),

  listInstances: () =>
    request<{ instances: Instance[] }>("/api/v1/instances").then((r) => r.instances),

  startInstance: (id: string) =>
    request<{ instance: Instance }>(`/api/v1/instances/${id}/start`, { method: "POST" })
      .then((r) => r.instance),

  stopInstance: (id: string) =>
    request<{ instance: Instance }>(`/api/v1/instances/${id}/stop`, { method: "POST" })
      .then((r) => r.instance),

  deleteInstance: (id: string) =>
    request<void>(`/api/v1/instances/${id}`, { method: "DELETE" }),

  createSession: (instanceId: string) =>
    request<{ session: Session }>("/api/v1/sessions", {
      method: "POST",
      body: JSON.stringify({ instanceId, ownerClientId: crypto.randomUUID() }),
    }).then((r) => r.session),

  getSession: (sessionId: string) =>
    request<{ session: Session }>(`/api/v1/sessions/${sessionId}`).then((r) => r.session),

  sendOffer: (sessionId: string, attachmentId: string, sdpOffer: string) =>
    request<{ sdpAnswer: string }>("/api/v1/signaling/session", {
      method: "POST",
      body: JSON.stringify({ sessionId, attachmentId, sdpOffer }),
    }),

  sendICECandidate: (sessionId: string, attachmentId: string, candidate: string) =>
    request<void>("/api/v1/signaling/ice-candidate", {
      method: "POST",
      body: JSON.stringify({ sessionId, attachmentId, candidate }),
    }),

  createInvite: (sessionId: string) =>
    request<{ token: string; inviteUrl: string }>(`/api/v1/sessions/${sessionId}/invites`, {
      method: "POST",
    }),

  promoteAttachment: (sessionId: string, attachmentId: string) =>
    request<void>(`/api/v1/sessions/${sessionId}/attachments/${attachmentId}/promote`, {
      method: "POST",
    }),

  demoteAttachment: (sessionId: string, attachmentId: string) =>
    request<void>(`/api/v1/sessions/${sessionId}/attachments/${attachmentId}/demote`, {
      method: "POST",
    }),
}
