import { useEffect, useRef, useState, useCallback } from "react"
import { useParams, useSearchParams } from "react-router-dom"
import {
  Layout, Typography, Tag, List, Button, Space, message, Spin,
} from "antd"
import { api, type Session as SessionData, type SessionAttachment } from "../api/client.js"

const { Header, Content, Sider } = Layout
const { Title, Text } = Typography

const ROLE_COLOR: Record<string, string> = {
  owner: "gold",
  participant: "blue",
  observer: "default",
}

function waitForIceGatheringComplete(pc: RTCPeerConnection, signal: AbortSignal): Promise<void> {
  if (pc.iceGatheringState === "complete") return Promise.resolve()

  return new Promise((resolve, reject) => {
    let timeoutId: number | undefined

    const cleanup = () => {
      if (timeoutId != null) window.clearTimeout(timeoutId)
      pc.removeEventListener("icegatheringstatechange", onIceGatheringStateChange)
      signal.removeEventListener("abort", onAbort)
    }
    const onIceGatheringStateChange = () => {
      if (pc.iceGatheringState !== "complete") return
      cleanup()
      resolve()
    }
    const onAbort = () => {
      cleanup()
      reject(new Error("WebRTC setup cancelled"))
    }

    pc.addEventListener("icegatheringstatechange", onIceGatheringStateChange)
    signal.addEventListener("abort", onAbort)
    timeoutId = window.setTimeout(() => {
      cleanup()
      reject(new Error("ICE gathering timed out after 10 seconds"))
    }, 10_000)

    if (signal.aborted) onAbort()
    else onIceGatheringStateChange()
  })
}

export default function Session() {
  const { sessionId } = useParams<{ sessionId: string }>()
  const [searchParams] = useSearchParams()
  const [messageApi, contextHolder] = message.useMessage()

  // attachmentId comes from sessionStorage (owner) or query param (invited client)
  const attachmentId = useRef<string | null>(
    sessionStorage.getItem(`attachment:${sessionId}`) ?? searchParams.get("attachmentId"),
  )

  const [session, setSession] = useState<SessionData | null>(null)
  const [myRole, setMyRole] = useState<string>("observer")
  const [connected, setConnected] = useState(false)

  const videoRef = useRef<HTMLVideoElement>(null)
  const pcRef = useRef<RTCPeerConnection | null>(null)
  const dcRef = useRef<RTCDataChannel | null>(null)

  const fetchSession = useCallback(() => {
    if (sessionId == null) return
    api.getSession(sessionId).then((s) => {
      setSession(s)
      const mine = s.attachments.find((a) => a.id === attachmentId.current)
      if (mine != null) setMyRole(mine.role)
    }).catch(() => {})
  }, [sessionId])

  useEffect(() => {
    fetchSession()
    const t = setInterval(fetchSession, 5000)
    return () => clearInterval(t)
  }, [fetchSession])

  // Start WebRTC once we have sessionId + attachmentId.
  useEffect(() => {
    const currentAttachmentId = attachmentId.current
    if (sessionId == null || currentAttachmentId == null) return
    let cancelled = false
    const abortController = new AbortController()
    const disconnectPath = `/api/v1/sessions/${encodeURIComponent(sessionId)}/attachments/${encodeURIComponent(currentAttachmentId)}/disconnect`

    const pc = new RTCPeerConnection({
      iceServers: [{ urls: "stun:stun.l.google.com:19302" }],
    })
    pcRef.current = pc

    const dc = pc.createDataChannel("input")
    dcRef.current = dc
    dc.onopen = () => setConnected(true)
    dc.onclose = () => setConnected(false)

    pc.ontrack = (ev) => {
      if (videoRef.current != null && ev.streams[0] != null) {
        videoRef.current.srcObject = ev.streams[0]
      }
    }

    const disconnectOnPagehide = () => {
      if (typeof navigator.sendBeacon === "function" && navigator.sendBeacon(disconnectPath)) return
      void api.disconnectAttachment(sessionId, currentAttachmentId).catch(() => {})
    }
    window.addEventListener("pagehide", disconnectOnPagehide)

    const setup = async () => {
      try {
        const offer = await pc.createOffer()
        if (cancelled) return
        await pc.setLocalDescription(offer)
        await waitForIceGatheringComplete(pc, abortController.signal)
        if (cancelled) return
        const sdpOffer = pc.localDescription?.sdp
        if (sdpOffer == null) throw new Error("Local SDP is unavailable")
        const { sdpAnswer } = await api.sendOffer(sessionId, currentAttachmentId, sdpOffer)
        if (cancelled) return
        await pc.setRemoteDescription({ type: "answer", sdp: sdpAnswer })
      } catch (error) {
        if (!cancelled) {
          const detail = error instanceof Error ? error.message : String(error)
          messageApi.error(`WebRTC setup failed: ${detail}`)
        }
      }
    }
    void setup()

    return () => {
      cancelled = true
      abortController.abort()
      window.removeEventListener("pagehide", disconnectOnPagehide)
      pc.close()
    }
  }, [sessionId, messageApi])

  const sendInput = useCallback((ev: React.MouseEvent<HTMLVideoElement>, action: string) => {
    if (myRole === "observer") return
    const dc = dcRef.current
    if (dc == null || dc.readyState !== "open") return
    const rect = (ev.target as HTMLElement).getBoundingClientRect()
    const x = (ev.clientX - rect.left) / rect.width
    const y = (ev.clientY - rect.top) / rect.height
    dc.send(JSON.stringify({ type: "touch", action, x, y }))
  }, [myRole])

  const handleInvite = async () => {
    if (sessionId == null) return
    try {
      const { inviteUrl } = await api.createInvite(sessionId)
      await navigator.clipboard.writeText(inviteUrl)
      messageApi.success("Invite URL copied to clipboard")
    } catch {
      messageApi.error("Failed to create invite")
    }
  }

  const handlePromote = async (att: SessionAttachment) => {
    if (sessionId == null) return
    await api.promoteAttachment(sessionId, att.id).catch((e: Error) => messageApi.error(e.message))
    fetchSession()
  }

  const handleDemote = async (att: SessionAttachment) => {
    if (sessionId == null) return
    await api.demoteAttachment(sessionId, att.id).catch((e: Error) => messageApi.error(e.message))
    fetchSession()
  }

  return (
    <Layout style={{ minHeight: "100vh" }}>
      {contextHolder}
      <Header style={{ display: "flex", alignItems: "center", gap: 12 }}>
        <Title level={4} style={{ color: "#fff", margin: 0 }}>mako</Title>
        <Tag color={connected ? "success" : "default"}>{connected ? "live" : "connecting…"}</Tag>
        <Tag color={ROLE_COLOR[myRole] ?? "default"}>{myRole}</Tag>
      </Header>
      <Layout>
        <Content style={{ background: "#000", display: "flex", alignItems: "center", justifyContent: "center" }}>
          <video
            ref={videoRef}
            autoPlay
            playsInline
            muted
            style={{ maxWidth: "100%", maxHeight: "calc(100vh - 64px)", cursor: myRole === "observer" ? "default" : "crosshair" }}
            onMouseDown={(e) => sendInput(e, "down")}
            onMouseUp={(e) => sendInput(e, "up")}
            onMouseMove={(e) => { if (e.buttons > 0) sendInput(e, "move") }}
          />
          {!connected && (
            <div style={{ position: "absolute", color: "#fff" }}>
              <Spin size="large" />
            </div>
          )}
        </Content>
        <Sider width={200} style={{ background: "#0f1923", padding: 16 }}>
          <Text style={{ color: "#aaa", fontSize: 12 }}>CONNECTED</Text>
          <List
            size="small"
            dataSource={session?.attachments ?? []}
            renderItem={(att) => (
              <List.Item style={{ padding: "4px 0", borderBottom: "none" }}>
                <Space direction="vertical" size={2} style={{ width: "100%" }}>
                  <Space>
                    <Tag color={ROLE_COLOR[att.role] ?? "default"} style={{ marginRight: 0 }}>
                      {att.role}
                    </Tag>
                    <Text style={{ color: att.id === attachmentId.current ? "#4a9eff" : "#ccc", fontSize: 12 }}>
                      {att.id === attachmentId.current ? "you" : att.clientId.slice(0, 8)}
                    </Text>
                  </Space>
                  {myRole === "owner" && att.id !== attachmentId.current && (
                    <Space size={4}>
                      {att.role === "observer" && (
                        <Button size="small" style={{ fontSize: 10, height: 20 }} onClick={() => handlePromote(att)}>
                          ↑ Promote
                        </Button>
                      )}
                      {att.role === "participant" && (
                        <Button size="small" style={{ fontSize: 10, height: 20 }} onClick={() => handleDemote(att)}>
                          ↓ Demote
                        </Button>
                      )}
                    </Space>
                  )}
                </Space>
              </List.Item>
            )}
          />
          <div style={{ marginTop: 16 }}>
            <Button block size="small" onClick={handleInvite}>Copy Invite</Button>
          </div>
        </Sider>
      </Layout>
    </Layout>
  )
}
