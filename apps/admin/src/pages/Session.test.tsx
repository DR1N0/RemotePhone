import { render, waitFor } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import Session from "./Session.js"

const { api } = vi.hoisted(() => ({
  api: {
    getSession: vi.fn(),
    sendOffer: vi.fn(),
    disconnectAttachment: vi.fn(),
    createInvite: vi.fn(),
    promoteAttachment: vi.fn(),
    demoteAttachment: vi.fn(),
  },
}))

vi.mock("../api/client.js", () => ({ api }))

class MockDataChannel {
  readyState: RTCDataChannelState = "connecting"
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  send = vi.fn()
}

class MockRTCPeerConnection {
  static instances: MockRTCPeerConnection[] = []

  iceGatheringState: RTCIceGatheringState = "gathering"
  localDescription: RTCSessionDescriptionInit | null = null
  ontrack: ((event: RTCTrackEvent) => void) | null = null
  createDataChannel = vi.fn(() => new MockDataChannel())
  createOffer = vi.fn(async (): Promise<RTCSessionDescriptionInit> => ({ type: "offer", sdp: "offer-sdp" }))
  setLocalDescription = vi.fn(async (description: RTCSessionDescriptionInit) => {
    this.localDescription = description
  })
  setRemoteDescription = vi.fn(async () => {})
  close = vi.fn()

  private listeners = new Map<string, Set<EventListener>>()

  constructor() {
    MockRTCPeerConnection.instances.push(this)
  }

  addEventListener(type: string, listener: EventListener) {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>()
    listeners.add(listener)
    this.listeners.set(type, listeners)
  }

  removeEventListener(type: string, listener: EventListener) {
    this.listeners.get(type)?.delete(listener)
  }

  completeIceGathering() {
    this.iceGatheringState = "complete"
    for (const listener of this.listeners.get("icegatheringstatechange") ?? []) {
      listener(new Event("icegatheringstatechange"))
    }
  }
}

function renderSession() {
  sessionStorage.setItem("attachment:session-1", "att-1")
  return render(
    <MemoryRouter initialEntries={["/sessions/session-1"]}>
      <Routes>
        <Route path="/sessions/:sessionId" element={<Session />} />
      </Routes>
    </MemoryRouter>,
  )
}

function fireIceGatheringComplete() {
  MockRTCPeerConnection.instances[0]?.completeIceGathering()
}

describe("Session", () => {
  beforeEach(() => {
    MockRTCPeerConnection.instances = []
    vi.stubGlobal("RTCPeerConnection", MockRTCPeerConnection)
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: false, addListener: vi.fn(), removeListener: vi.fn() }))
    Object.defineProperty(navigator, "sendBeacon", { configurable: true, value: undefined })
    api.getSession.mockResolvedValue({
      id: "session-1",
      instanceId: "instance-1",
      status: "active",
      attachments: [{
        id: "att-1",
        sessionId: "session-1",
        clientId: "client-1",
        role: "owner",
        status: "connected",
      }],
    })
    api.sendOffer.mockResolvedValue({ sdpAnswer: "answer-sdp" })
    api.disconnectAttachment.mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.clearAllMocks()
    vi.unstubAllGlobals()
  })

  it("submits the offer only after ICE gathering completes", async () => {
    renderSession()

    await waitFor(() => expect(MockRTCPeerConnection.instances).toHaveLength(1))
    expect(api.sendOffer).not.toHaveBeenCalled()

    fireIceGatheringComplete()

    await waitFor(() => expect(api.sendOffer).toHaveBeenCalledOnce())
    expect(api.sendOffer).toHaveBeenCalledWith("session-1", "att-1", "offer-sdp")
  })

  it("disconnects its attachment on pagehide", async () => {
    renderSession()

    await waitFor(() => expect(MockRTCPeerConnection.instances).toHaveLength(1))
    window.dispatchEvent(new Event("pagehide"))

    expect(api.disconnectAttachment).toHaveBeenCalledWith("session-1", "att-1")
  })

  it("surfaces an offer failure without retrying", async () => {
    api.sendOffer.mockRejectedValueOnce(new Error("worker unavailable"))
    renderSession()

    await waitFor(() => expect(MockRTCPeerConnection.instances).toHaveLength(1))
    fireIceGatheringComplete()

    await waitFor(() => expect(api.sendOffer).toHaveBeenCalledOnce())
    await waitFor(() => expect(document.body).toHaveTextContent("WebRTC setup failed: worker unavailable"))
    expect(api.sendOffer).toHaveBeenCalledOnce()
  })
})
