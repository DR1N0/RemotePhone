import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { MemoryRouter, Route, Routes } from "react-router-dom"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import Session from "./Session.js"

const { api } = vi.hoisted(() => ({
  api: {
    getDiagnosticSession: vi.fn(),
    sendDiagnosticOffer: vi.fn(),
    sendDiagnosticICECandidate: vi.fn(),
    disconnectDiagnosticAttachment: vi.fn(),
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
  onicecandidate: ((event: RTCPeerConnectionIceEvent) => void) | null = null
  addTransceiver = vi.fn()
  createDataChannel = vi.fn(() => new MockDataChannel())
  createOffer = vi.fn(async (): Promise<RTCSessionDescriptionInit> => ({ type: "offer", sdp: "offer-sdp" }))
  setLocalDescription = vi.fn(async (description: RTCSessionDescriptionInit) => { this.localDescription = description })
  setRemoteDescription = vi.fn(async () => {})
  close = vi.fn()
  private listeners = new Map<string, Set<EventListener>>()

  constructor() { MockRTCPeerConnection.instances.push(this) }
  addEventListener(type: string, listener: EventListener) {
    const listeners = this.listeners.get(type) ?? new Set<EventListener>()
    listeners.add(listener)
    this.listeners.set(type, listeners)
  }
  removeEventListener(type: string, listener: EventListener) { this.listeners.get(type)?.delete(listener) }
  completeIceGathering() {
    this.iceGatheringState = "complete"
    for (const listener of this.listeners.get("icegatheringstatechange") ?? []) listener(new Event("icegatheringstatechange"))
  }
  emitIceCandidate(candidate: string) {
    this.onicecandidate?.({ candidate: { candidate } } as RTCPeerConnectionIceEvent)
  }
}

function renderSession(route = "/sessions/session-1") {
  return render(
    <MemoryRouter initialEntries={[route]}>
      <Routes><Route path="/sessions/:sessionId" element={<Session />} /></Routes>
    </MemoryRouter>,
  )
}

function connectDiagnostic() {
  fireEvent.change(screen.getByLabelText("Attachment ID"), { target: { value: "att-1" } })
  fireEvent.change(screen.getByLabelText("Attachment credential"), { target: { value: "attachment-token" } })
  fireEvent.click(screen.getByRole("button", { name: "Connect diagnostic" }))
}

describe("Session diagnostic", () => {
  beforeEach(() => {
    MockRTCPeerConnection.instances = []
    vi.stubGlobal("RTCPeerConnection", MockRTCPeerConnection)
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: false, addListener: vi.fn(), removeListener: vi.fn() }))
    api.getDiagnosticSession.mockResolvedValue({ id: "session-1", instanceId: "instance-1", status: "active", attachments: [{ id: "att-1", sessionId: "session-1", clientId: "client-1", role: "owner", status: "connected" }] })
    api.sendDiagnosticOffer.mockResolvedValue({ sdpAnswer: "answer-sdp" })
    api.sendDiagnosticICECandidate.mockResolvedValue(undefined)
    api.disconnectDiagnosticAttachment.mockResolvedValue(undefined)
  })

  afterEach(() => { vi.clearAllMocks(); vi.unstubAllGlobals(); sessionStorage.clear() })

  it("requires a pasted attachment credential and ignores attachment IDs from the query string", async () => {
    renderSession("/sessions/session-1?attachmentId=att-query")

    expect(screen.getByLabelText("Attachment credential")).toBeInTheDocument()
    expect(MockRTCPeerConnection.instances).toHaveLength(0)
    expect(api.getDiagnosticSession).not.toHaveBeenCalled()

    connectDiagnostic()
    await waitFor(() => expect(api.getDiagnosticSession).toHaveBeenCalledWith("session-1", "attachment-token"))
    await waitFor(() => expect(MockRTCPeerConnection.instances).toHaveLength(1))
  })

  it("uses the bound attachment credential for SDP and authenticated keepalive disconnect", async () => {
    renderSession()
    connectDiagnostic()
    await waitFor(() => expect(MockRTCPeerConnection.instances).toHaveLength(1))

    MockRTCPeerConnection.instances[0]!.completeIceGathering()
    await waitFor(() => expect(api.sendDiagnosticOffer).toHaveBeenCalledWith("session-1", "att-1", "offer-sdp", "attachment-token"))

    window.dispatchEvent(new Event("pagehide"))
    expect(api.disconnectDiagnosticAttachment).toHaveBeenCalledWith("session-1", "att-1", "attachment-token", { keepalive: true })
  })

  it("queues ICE until the attachment transport is established, then forwards it with its bound bearer", async () => {
    renderSession()
    connectDiagnostic()
    await waitFor(() => expect(MockRTCPeerConnection.instances).toHaveLength(1))

    const pc = MockRTCPeerConnection.instances[0]!
    pc.emitIceCandidate("candidate:before-answer")
    expect(api.sendDiagnosticICECandidate).not.toHaveBeenCalled()

    pc.completeIceGathering()
    await waitFor(() => expect(api.sendDiagnosticOffer).toHaveBeenCalledWith("session-1", "att-1", "offer-sdp", "attachment-token"))
    await waitFor(() => expect(api.sendDiagnosticICECandidate).toHaveBeenCalledWith("session-1", "att-1", "candidate:before-answer", "attachment-token"))
    expect(api.sendDiagnosticOffer.mock.invocationCallOrder[0]!).toBeLessThan(api.sendDiagnosticICECandidate.mock.invocationCallOrder[0]!)
  })
})
