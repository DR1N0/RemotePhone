import type { LaunchConfig, RuntimeHandle, RuntimeClass } from "../domain/types.js"

export type TransportMode = "prototype-webrtc"
export type TransportStatus = "stub"

export type InstalledRuntime = {
  runtimeClass: string
  displayName: string
}

export type WorkerRecord = {
  id: string
  kind: "local"
  hostOs: string
  arch: string
  health: string
  installedRuntimes: InstalledRuntime[]
}

export type WorkerTemplate = {
  templateId: string
  runtimeClass: string
  displayName: string
  metadata?: Record<string, string>
}

export type StartInstanceInput = {
  instanceId: string
  runtimeClass: RuntimeClass
  templateId: string
  launchConfig: LaunchConfig
}

export type WorkerInstanceResponse = {
  state: string
  handle: RuntimeHandle
  lastError?: string
}

export type CreateTransportSessionInput = {
  sessionId: string
  attachmentId: string
  instanceId: string
  runtimeClass: RuntimeClass
  runtimeHandle: RuntimeHandle
}

export type WorkerTransportSession = {
  sessionId: string
  attachmentId: string
  instanceId: string
  runtimeClass: RuntimeClass
  mode: TransportMode
  status: TransportStatus
  workerBaseUrl: string
  sessionToken: string
}

export interface DiscoveryWorkerClient {
  listWorkers(): Promise<WorkerRecord[]>
  listTemplates(): Promise<WorkerTemplate[]>
  createTransportSession?(input: CreateTransportSessionInput): Promise<WorkerTransportSession>
}

export interface ManagedInstanceWorkerClient extends DiscoveryWorkerClient {
  startInstance(input: StartInstanceInput): Promise<WorkerInstanceResponse>
  inspectInstance(instanceId: string): Promise<WorkerInstanceResponse>
  stopInstance(instanceId: string): Promise<WorkerInstanceResponse>
  createTransportSession(input: CreateTransportSessionInput): Promise<WorkerTransportSession>
}

type WorkerHealthResponse = {
  status: string
  workerId?: string
  hostOs?: string
  arch?: string
  installedRuntimes?: InstalledRuntime[]
}

type TemplateListResponse = WorkerTemplate[] | {
  templates?: WorkerTemplate[]
}

async function readJson<T>(response: Response): Promise<T> {
  if (!response.ok) {
    throw new Error(`Worker request failed with status ${response.status}`)
  }

  return await response.json() as T
}

export class LocalWorkerClient implements ManagedInstanceWorkerClient {
  constructor(
    private readonly baseUrl: string,
    private readonly fetchImpl: typeof fetch = globalThis.fetch,
  ) {}

  async listWorkers(): Promise<WorkerRecord[]> {
    const response = await this.fetchImpl(`${this.baseUrl}/healthz`)
    const body = await readJson<WorkerHealthResponse>(response)

    return [
      {
        id: body.workerId ?? "local",
        kind: "local",
        hostOs: body.hostOs ?? "unknown",
        arch: body.arch ?? "unknown",
        health: body.status,
        installedRuntimes: body.installedRuntimes ?? [],
      },
    ]
  }

  async listTemplates(): Promise<WorkerTemplate[]> {
    const response = await this.fetchImpl(`${this.baseUrl}/v1/templates`)
    const body = await readJson<TemplateListResponse>(response)

    if (Array.isArray(body)) {
      return body
    }

    return body.templates ?? []
  }

  async startInstance(input: StartInstanceInput): Promise<WorkerInstanceResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/v1/instances/start`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
      },
      body: JSON.stringify(input),
    })

    return await readJson<WorkerInstanceResponse>(response)
  }

  async inspectInstance(instanceId: string): Promise<WorkerInstanceResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/v1/instances/${encodeURIComponent(instanceId)}`)
    return await readJson<WorkerInstanceResponse>(response)
  }

  async stopInstance(instanceId: string): Promise<WorkerInstanceResponse> {
    const response = await this.fetchImpl(`${this.baseUrl}/v1/instances/${encodeURIComponent(instanceId)}/stop`, {
      method: "POST",
    })

    return await readJson<WorkerInstanceResponse>(response)
  }

  async createTransportSession(input: CreateTransportSessionInput): Promise<WorkerTransportSession> {
    const response = await this.fetchImpl(`${this.baseUrl}/v1/transport/session`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
      },
      body: JSON.stringify(input),
    })

    return await readJson<WorkerTransportSession>(response)
  }
}
