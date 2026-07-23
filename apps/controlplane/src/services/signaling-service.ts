import type { ManagedInstanceWorkerClient } from "./worker-client.js"

export type SignalingService = ReturnType<typeof createSignalingService>

export function createSignalingService(workerClient: ManagedInstanceWorkerClient) {
  return {
    async relayOffer(input: {
      instanceId: string
      attachmentId: string
      role: string
      sdpOffer: string
    }): Promise<{ sdpAnswer: string }> {
      return workerClient.forwardOffer({
        instanceId: input.instanceId,
        attachmentId: input.attachmentId,
        role: input.role,
        sdpOffer: input.sdpOffer,
      })
    },

    async relayICECandidate(input: {
      instanceId: string
      attachmentId: string
      candidate: string
    }): Promise<void> {
      return workerClient.forwardICECandidate(input)
    },
  }
}
