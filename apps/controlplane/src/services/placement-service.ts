import type { PlacementPolicy } from "../domain/types.js"
import type { WorkerRecord } from "./worker-client.js"

export class PlacementSelectionError extends Error {}

export type PlacementSelection = {
  workerId: string
}

export function selectPlacement(policy: PlacementPolicy, workers: WorkerRecord[]): PlacementSelection {
  if (policy.kind !== "local-only") {
    throw new PlacementSelectionError(`placement policy kind ${policy.kind} is not supported in v1`)
  }

  const localWorker = workers.find((worker) => worker.kind === "local")
  if (localWorker == null) {
    throw new PlacementSelectionError("no local worker is available for local-only placement")
  }

  if (localWorker.health !== "ok") {
    throw new PlacementSelectionError(`worker ${localWorker.id} is not healthy enough for placement`)
  }

  return {
    workerId: localWorker.id,
  }
}
