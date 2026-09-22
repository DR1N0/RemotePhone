import { randomUUID } from "node:crypto"

import type { AppDb } from "../persistence/db.js"
import {
  createInstance,
  deleteInstanceById,
  getInstanceById,
  listInstances,
  listInstancesByOwner,
  updateInstanceLifecycle,
} from "../persistence/repositories/instances.js"
import { getPrincipalById } from "../persistence/repositories/principals.js"
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
export class InstanceForbiddenError extends InstanceServiceError {}

export type ManagedInstanceRecord = {
  id: string
  name: string
  ownerId: string
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
    ownerId: row.ownerId,
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

function assertCanManageInstance(db: AppDb, instance: InstanceRow, callerPrincipalId: string): void {
  if (instance.ownerId === callerPrincipalId) {
    return
  }
  const caller = getPrincipalById(db, callerPrincipalId)
  if (caller?.isServerOwner === true) {
    return
  }
  throw new InstanceForbiddenError(`principal ${callerPrincipalId} may not manage instance ${instance.id}`)
}

export function createInstanceService(deps: {
  db: AppDb
  workerClient: ManagedInstanceWorkerClient
}) {
  const { db, workerClient } = deps

  return {
    async createManagedInstance(input: CreateInstanceRequest, ownerId: string): Promise<ManagedInstanceRecord> {
      const [template] = await Promise.all([
        resolveTemplate(workerClient, input.templateId),
        validatePlacement(workerClient, input),
      ])

      const id = randomUUID()
      createInstance(db, {
        id,
        ownerId,
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

    async listManagedInstances(callerPrincipalId: string): Promise<ManagedInstanceRecord[]> {
      const caller = getPrincipalById(db, callerPrincipalId)
      const rows = caller?.isServerOwner === true
        ? listInstances(db)
        : listInstancesByOwner(db, callerPrincipalId)
      return rows.map(toManagedInstance)
    },

    async getManagedInstance(id: string, callerPrincipalId: string): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerPrincipalId)
      return toManagedInstance(row)
    },

    async getManagedInstanceUnchecked(id: string): Promise<ManagedInstanceRecord> {
      return toManagedInstance(getRequiredInstance(db, id))
    },

    async startManagedInstance(id: string, callerPrincipalId: string): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerPrincipalId)
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

    async stopManagedInstance(id: string, callerPrincipalId: string): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerPrincipalId)

      if (row.runtimeHandle == null) {
        updateInstanceLifecycle(db, {
          id,
          desiredState: "stopped",
          observedState: "stopped",
          runtimeHandle: null,
          lastError: null,
        })
        return toManagedInstance(getRequiredInstance(db, id))
      }

      let response: Awaited<ReturnType<typeof workerClient.stopInstance>>
      try {
        response = await workerClient.stopInstance(id)
      } catch (error) {
        // Worker no longer has the handle — treat as already stopped
        updateInstanceLifecycle(db, {
          id,
          desiredState: "stopped",
          observedState: "stopped",
          runtimeHandle: null,
          lastError: null,
        })
        return toManagedInstance(getRequiredInstance(db, id))
      }

      updateInstanceLifecycle(db, {
        id,
        desiredState: "stopped",
        observedState: response.state === "" ? "stopped" : response.state as InstanceRow["observedState"],
        runtimeHandle: null,
        lastError: response.lastError ?? null,
      })

      return toManagedInstance(getRequiredInstance(db, id))
    },

    async stopManagedInstanceUnchecked(id: string): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)

      if (row.runtimeHandle == null) {
        updateInstanceLifecycle(db, {
          id,
          desiredState: "stopped",
          observedState: "stopped",
          runtimeHandle: null,
          lastError: null,
        })
        return toManagedInstance(getRequiredInstance(db, id))
      }

      let response: Awaited<ReturnType<typeof workerClient.stopInstance>>
      try {
        response = await workerClient.stopInstance(id)
      } catch (error) {
        // Worker no longer has the handle — treat as already stopped
        updateInstanceLifecycle(db, {
          id,
          desiredState: "stopped",
          observedState: "stopped",
          runtimeHandle: null,
          lastError: null,
        })
        return toManagedInstance(getRequiredInstance(db, id))
      }

      updateInstanceLifecycle(db, {
        id,
        desiredState: "stopped",
        observedState: response.state === "" ? "stopped" : response.state as InstanceRow["observedState"],
        runtimeHandle: null,
        lastError: response.lastError ?? null,
      })

      return toManagedInstance(getRequiredInstance(db, id))
    },

    async deleteManagedInstance(id: string, callerPrincipalId: string): Promise<void> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerPrincipalId)
      deleteInstanceById(db, id)
    },
  }
}
