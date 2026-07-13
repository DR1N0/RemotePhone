import { randomUUID } from "node:crypto"

import type { AppDb } from "../persistence/db.js"
import {
  createInstance,
  deleteInstanceById,
  getInstanceById,
  listInstances,
  updateInstanceLifecycle,
} from "../persistence/repositories/instances.js"
import type { InstanceRow } from "../persistence/schema.js"
import { selectPlacement } from "./placement-service.js"
import type {
  ManagedInstanceWorkerClient,
  WorkerTemplate,
} from "./worker-client.js"
import type { CreateInstanceRequest, RuntimeClass } from "../domain/types.js"

export class InstanceServiceError extends Error {}
export class InstanceNotFoundError extends InstanceServiceError {}
export class InvalidRequestError extends InstanceServiceError {}

export type ManagedInstanceRecord = {
  id: string
  name: string
  runtimeClass: RuntimeClass
  templateId: string
  placement: InstanceRow["placementPolicy"]
  resourceProfile: InstanceRow["resourceProfile"]
  launchConfig: InstanceRow["launchConfig"]
  desiredState: InstanceRow["desiredState"]
  observedState: InstanceRow["observedState"]
  runtimeHandle: InstanceRow["runtimeHandle"]
  stateDataRef: string | null
  lastError: string | null
  createdAt: string
  updatedAt: string
}

export type InstanceService = ReturnType<typeof createInstanceService>

function toManagedInstance(row: InstanceRow): ManagedInstanceRecord {
  return {
    id: row.id,
    name: row.name,
    runtimeClass: row.runtimeClass,
    templateId: row.templateId,
    placement: row.placementPolicy,
    resourceProfile: row.resourceProfile,
    launchConfig: row.launchConfig,
    desiredState: row.desiredState,
    observedState: row.observedState,
    runtimeHandle: row.runtimeHandle,
    stateDataRef: row.stateDataRef,
    lastError: row.lastError,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
  }
}

function toInvalidRequestError(error: unknown): InvalidRequestError {
  if (error instanceof InvalidRequestError) {
    return error
  }
  return new InvalidRequestError(error instanceof Error ? error.message : String(error))
}

async function resolveTemplate(
  workerClient: ManagedInstanceWorkerClient,
  templateId: string,
): Promise<WorkerTemplate> {
  const templates = await workerClient.listTemplates()
  const template = templates.find((candidate) => candidate.templateId === templateId)
  if (template == null) {
    throw new InvalidRequestError(`runtime template not found: ${templateId}`)
  }
  return template
}

async function validatePlacement(
  workerClient: ManagedInstanceWorkerClient,
  input: CreateInstanceRequest,
): Promise<void> {
  const workers = await workerClient.listWorkers()
  try {
    selectPlacement(input.placement, workers)
  } catch (error) {
    throw toInvalidRequestError(error)
  }
}

function getRequiredInstance(db: AppDb, id: string): InstanceRow {
  const row = getInstanceById(db, id)
  if (row == null) {
    throw new InstanceNotFoundError(`managed instance not found: ${id}`)
  }
  return row
}

export function createInstanceService(deps: {
  db: AppDb
  workerClient: ManagedInstanceWorkerClient
}) {
  const { db, workerClient } = deps

  return {
    async createManagedInstance(input: CreateInstanceRequest): Promise<ManagedInstanceRecord> {
      const [template] = await Promise.all([
        resolveTemplate(workerClient, input.templateId),
        validatePlacement(workerClient, input),
      ])

      const id = randomUUID()
      createInstance(db, {
        id,
        name: input.name,
        runtimeClass: template.runtimeClass as RuntimeClass,
        templateId: template.templateId,
        placementPolicy: input.placement,
        resourceProfile: input.resourceProfile,
        launchConfig: input.launchConfig,
        desiredState: "stopped",
        observedState: "stopped",
        runtimeHandle: null,
        stateDataRef: null,
        lastError: null,
      })

      return toManagedInstance(getRequiredInstance(db, id))
    },

    async listManagedInstances(): Promise<ManagedInstanceRecord[]> {
      return listInstances(db).map(toManagedInstance)
    },

    async getManagedInstance(id: string): Promise<ManagedInstanceRecord> {
      return toManagedInstance(getRequiredInstance(db, id))
    },

    async startManagedInstance(id: string): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)
      const response = await workerClient.startInstance({
        instanceId: row.id,
        runtimeClass: row.runtimeClass,
        templateId: row.templateId,
        launchConfig: row.launchConfig,
      })

      updateInstanceLifecycle(db, {
        id: row.id,
        desiredState: "running",
        observedState: response.state === "" ? "starting" : response.state as InstanceRow["observedState"],
        runtimeHandle: response.handle,
        lastError: response.lastError ?? null,
      })

      return toManagedInstance(getRequiredInstance(db, id))
    },

    async stopManagedInstance(id: string): Promise<ManagedInstanceRecord> {
      getRequiredInstance(db, id)
      const response = await workerClient.stopInstance(id)

      updateInstanceLifecycle(db, {
        id,
        desiredState: "stopped",
        observedState: response.state === "" ? "stopped" : response.state as InstanceRow["observedState"],
        runtimeHandle: null,
        lastError: response.lastError ?? null,
      })

      return toManagedInstance(getRequiredInstance(db, id))
    },

    async deleteManagedInstance(id: string): Promise<void> {
      getRequiredInstance(db, id)
      deleteInstanceById(db, id)
    },
  }
}
