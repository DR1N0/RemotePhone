import type { ManagedInstanceWorkerClient } from "./worker-client.js"

export type SignalingService = ReturnType<typeof createSignalingService>

export function createSignalingService(workerClient: ManagedInstanceWorkerClient) {
  return {
    async relayOffer(input: {
      instanceId: string
      attachmentId: string
      sdpOffer: string
    }): Promise<{ sdpAnswer: string }> {
      return workerClient.forwardOffer({
        instanceId: input.instanceId,
        attachmentId: input.attachmentId,
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
