import { randomUUID } from "node:crypto"

import type { AppDb } from "../persistence/db.js"
import {
  createInstanceIfOwnerAbsent,
  deleteInstanceById,
  getInstanceById,
  listInstances,
  listInstancesByOwner,
  updateInstanceLifecycle,
} from "../persistence/repositories/instances.js"
import { getUserById } from "../persistence/repositories/users.js"
import type { InstanceRow } from "../persistence/schema.js"
import { selectPlacement } from "./placement-service.js"
import { createAuditService, type AuditContext, type AuditService } from "./audit-service.js"
import type {
  ManagedInstanceWorkerClient,
  WorkerTemplate,
} from "./worker-client.js"
import type { CreateInstanceRequest, RuntimeClass } from "../domain/types.js"

export class InstanceServiceError extends Error {}
export class InstanceNotFoundError extends InstanceServiceError {}
export class InvalidRequestError extends InstanceServiceError {}
export class InstanceForbiddenError extends InstanceServiceError {}
export class InstanceConflictError extends InstanceServiceError {}
export class AuditTerminalOutcomeUnavailableError extends InstanceServiceError {}

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

function assertCanCreateInstance(db: AppDb, ownerId: string): void {
  const owner = getUserById(db, ownerId)
  if (
    owner == null
    || owner.userType === "guest"
    || owner.status !== "active"
    || owner.enrollmentRequired
  ) {
    throw new InstanceForbiddenError("user may not create an Instance")
  }
}

function assertCanManageInstance(db: AppDb, instance: InstanceRow, callerUserId: string): void {
  if (instance.ownerId === callerUserId) {
    return
  }
  const caller = getUserById(db, callerUserId)
  if (caller?.userType === "admin") {
    return
  }
  throw new InstanceForbiddenError(`user ${callerUserId} may not manage instance ${instance.id}`)
}

export function createInstanceService(deps: {
  db: AppDb
  workerClient: ManagedInstanceWorkerClient
  auditService?: AuditService
}) {
  const { db, workerClient } = deps
  const auditService = deps.auditService ?? createAuditService({ db })

  function recordTerminalPersistenceFailure(
    audit: AuditContext,
    action: "instance.start" | "instance.stop",
    targetId: string,
    persistenceError: unknown,
  ): never {
    try {
      auditService.record({ ...audit, action, targetType: "instance", targetId, outcome: "failure" })
    } catch (auditError) {
      throw new AuditTerminalOutcomeUnavailableError(
        `Worker ${action} succeeded, but persistence and durable terminal audit recording both failed: ${
          auditError instanceof Error ? auditError.message : String(auditError)
        }`,
        { cause: persistenceError },
      )
    }
    throw persistenceError
  }

  return {
    async createManagedInstance(input: CreateInstanceRequest, ownerId: string): Promise<ManagedInstanceRecord> {
      assertCanCreateInstance(db, ownerId)
      const [template] = await Promise.all([
        resolveTemplate(workerClient, input.templateId),
        validatePlacement(workerClient, input),
      ])

      const id = randomUUID()
      const created = createInstanceIfOwnerAbsent(db, {
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
      if (!created) {
        throw new InstanceConflictError("user already owns an Instance")
      }

      return toManagedInstance(getRequiredInstance(db, id))
    },

    async listManagedInstances(callerUserId: string): Promise<ManagedInstanceRecord[]> {
      const caller = getUserById(db, callerUserId)
      const rows = caller?.userType === "admin"
        ? listInstances(db)
        : listInstancesByOwner(db, callerUserId)
      return rows.map(toManagedInstance)
    },

    async getManagedInstance(id: string, callerUserId: string): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerUserId)
      return toManagedInstance(row)
    },

    async getManagedInstanceUnchecked(id: string): Promise<ManagedInstanceRecord> {
      return toManagedInstance(getRequiredInstance(db, id))
    },

    async startManagedInstance(id: string, callerUserId: string, audit: AuditContext): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerUserId)
      auditService.record({ ...audit, action: "instance.start", targetType: "instance", targetId: row.id, outcome: "attempt" })
      let response: Awaited<ReturnType<typeof workerClient.startInstance>>
      try {
        response = await workerClient.startInstance({
          instanceId: row.id,
          runtimeClass: row.runtimeClass,
          templateId: row.templateId,
          launchConfig: row.launchConfig,
        })
      } catch (error) {
        auditService.record({ ...audit, action: "instance.start", targetType: "instance", targetId: row.id, outcome: "failure" })
        throw error
      }

      try {
        return db.$client.transaction(() => {
          updateInstanceLifecycle(db, {
            id: row.id,
            desiredState: "running",
            observedState: response.state === "" ? "starting" : response.state as InstanceRow["observedState"],
            runtimeHandle: response.handle,
            lastError: response.lastError ?? null,
          })
          auditService.record({ ...audit, action: "instance.start", targetType: "instance", targetId: row.id, outcome: "success" })
          return toManagedInstance(getRequiredInstance(db, id))
        })()
      } catch (error) {
        return recordTerminalPersistenceFailure(audit, "instance.start", row.id, error)
      }
    },

    async stopManagedInstance(id: string, callerUserId: string, audit: AuditContext): Promise<ManagedInstanceRecord> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerUserId)

      if (row.runtimeHandle == null) {
        return db.$client.transaction(() => {
          updateInstanceLifecycle(db, {
            id,
            desiredState: "stopped",
            observedState: "stopped",
            runtimeHandle: null,
            lastError: null,
          })
          auditService.record({ ...audit, action: "instance.stop", targetType: "instance", targetId: row.id, outcome: "success" })
          return toManagedInstance(getRequiredInstance(db, id))
        })()
      }

      auditService.record({ ...audit, action: "instance.stop", targetType: "instance", targetId: row.id, outcome: "attempt" })
      let response: Awaited<ReturnType<typeof workerClient.stopInstance>>
      try {
        response = await workerClient.stopInstance(id)
      } catch (error) {
        auditService.record({ ...audit, action: "instance.stop", targetType: "instance", targetId: row.id, outcome: "failure" })
        throw error
      }

      try {
        return db.$client.transaction(() => {
          updateInstanceLifecycle(db, {
            id,
            desiredState: "stopped",
            observedState: response.state === "" ? "stopped" : response.state as InstanceRow["observedState"],
            runtimeHandle: null,
            lastError: response.lastError ?? null,
          })
          auditService.record({ ...audit, action: "instance.stop", targetType: "instance", targetId: row.id, outcome: "success" })
          return toManagedInstance(getRequiredInstance(db, id))
        })()
      } catch (error) {
        return recordTerminalPersistenceFailure(audit, "instance.stop", row.id, error)
      }
    },

    async deleteManagedInstance(id: string, callerUserId: string, audit: AuditContext): Promise<void> {
      const row = getRequiredInstance(db, id)
      assertCanManageInstance(db, row, callerUserId)
      db.$client.transaction(() => {
        deleteInstanceById(db, id)
        auditService.record({ ...audit, action: "instance.delete", targetType: "instance", targetId: row.id, outcome: "success" })
      })()
    },
  }
}
