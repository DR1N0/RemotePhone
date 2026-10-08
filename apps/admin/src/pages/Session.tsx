import { useCallback, useEffect, useRef, useState } from "react"
import { useParams } from "react-router-dom"
import { Button, Form, Input, Layout, List, Space, Spin, Tag, Typography, message } from "antd"
import { api, type Session as SessionData } from "../api/client.js"

const { Header, Content, Sider } = Layout
const { Paragraph, Text, Title } = Typography
const ROLE_COLOR: Record<string, string> = { owner: "gold", participant: "blue", observer: "default" }

function credentialKey(sessionId: string) { return `mako.diagnostic.attachment-token:${sessionId}` }
function attachmentKey(sessionId: string) { return `mako.diagnostic.attachment-id:${sessionId}` }

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
    const onAbort = () => { cleanup(); reject(new Error("WebRTC setup cancelled")) }
    pc.addEventListener("icegatheringstatechange", onIceGatheringStateChange)
    signal.addEventListener("abort", onAbort)
    timeoutId = window.setTimeout(() => { cleanup(); reject(new Error("ICE gathering timed out after 10 seconds")) }, 10_000)
    if (signal.aborted) onAbort()
    else onIceGatheringStateChange()
  })
}

export default function Session() {
  const { sessionId } = useParams<{ sessionId: string }>()
  const [messageApi, contextHolder] = message.useMessage()
  const [form] = Form.useForm<{ attachmentId: string; token: string }>()
  const [attachmentToken, setAttachmentToken] = useState(() => sessionId == null ? null : sessionStorage.getItem(credentialKey(sessionId)))
  const [attachmentId, setAttachmentId] = useState(() => sessionId == null ? null : sessionStorage.getItem(attachmentKey(sessionId)))
  const [session, setSession] = useState<SessionData | null>(null)
  const [myRole, setMyRole] = useState<string>("observer")
  const [connected, setConnected] = useState(false)
  const hasBoundAttachment = attachmentId != null && session?.attachments.some((candidate) => candidate.id === attachmentId) === true
  const videoRef = useRef<HTMLVideoElement>(null)
  const pcRef = useRef<RTCPeerConnection | null>(null)
  const dcRef = useRef<RTCDataChannel | null>(null)

  useEffect(() => {
    if (sessionId != null) form.setFieldsValue({ attachmentId: attachmentId ?? "", token: attachmentToken ?? "" })
  }, [attachmentId, attachmentToken, form, sessionId])

  const fetchSession = useCallback(() => {
    if (sessionId == null || attachmentToken == null || attachmentId == null) return
    api.getDiagnosticSession(sessionId, attachmentToken).then((result) => {
      setSession(result)
      const attachment = result.attachments.find((candidate) => candidate.id === attachmentId)
      if (attachment == null) {
        messageApi.error("The attachment credential is not bound to this attachment ID")
        return
      }
      setMyRole(attachment.role)
    }).catch((error: unknown) => messageApi.error(error instanceof Error ? error.message : "Unable to load diagnostic session"))
  }, [attachmentId, attachmentToken, messageApi, sessionId])

  useEffect(() => {
    fetchSession()
    if (attachmentToken == null || attachmentId == null) return
    const timer = window.setInterval(fetchSession, 5000)
    return () => window.clearInterval(timer)
  }, [attachmentId, attachmentToken, fetchSession])

  useEffect(() => {
    if (sessionId == null || attachmentToken == null || attachmentId == null || !hasBoundAttachment) return
    let cancelled = false
    const abortController = new AbortController()
    const pc = new RTCPeerConnection({ iceServers: [{ urls: "stun:stun.l.google.com:19302" }] })
    pcRef.current = pc
    pc.addTransceiver("video", { direction: "recvonly" })
    const dc = pc.createDataChannel("input")
    dcRef.current = dc
    dc.onopen = () => setConnected(true)
    dc.onclose = () => setConnected(false)

    const queuedIceCandidates: string[] = []
    let signalingReady = false
    let flushingIceCandidates = false
    const flushIceCandidates = async () => {
      if (!signalingReady || flushingIceCandidates) return
      flushingIceCandidates = true
      try {
        while (!cancelled && queuedIceCandidates.length > 0) {
          const candidate = queuedIceCandidates.shift()
          if (candidate == null) continue
          await api.sendDiagnosticICECandidate(sessionId, attachmentId, candidate, attachmentToken)
        }
      } catch (error) {
        if (!cancelled) messageApi.error(`ICE forwarding failed: ${error instanceof Error ? error.message : String(error)}`)
      } finally {
        flushingIceCandidates = false
        if (!cancelled && signalingReady && queuedIceCandidates.length > 0) void flushIceCandidates()
      }
    }
    pc.onicecandidate = (event) => {
      const candidate = event.candidate?.candidate
      if (cancelled || candidate == null || candidate === "") return
      queuedIceCandidates.push(candidate)
      void flushIceCandidates()
    }
    pc.ontrack = (event) => { if (videoRef.current != null && event.streams[0] != null) videoRef.current.srcObject = event.streams[0] }
    const disconnectOnPagehide = () => {
      void api.disconnectDiagnosticAttachment(sessionId, attachmentId, attachmentToken, { keepalive: true }).catch(() => {})
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
        const { sdpAnswer } = await api.sendDiagnosticOffer(sessionId, attachmentId, sdpOffer, attachmentToken)
        if (cancelled) return
        await pc.setRemoteDescription({ type: "answer", sdp: sdpAnswer })
        if (cancelled) return
        // The offer endpoint ensures the attachment transport before returning. Candidates
        // collected while ICE was gathering are sent only after that adapter is ready.
        signalingReady = true
        await flushIceCandidates()
      } catch (error) {
        if (!cancelled) messageApi.error(`WebRTC setup failed: ${error instanceof Error ? error.message : String(error)}`)
      }
    }
    void setup()
    return () => {
      cancelled = true
      abortController.abort()
      queuedIceCandidates.length = 0
      pc.onicecandidate = null
      window.removeEventListener("pagehide", disconnectOnPagehide)
      pc.close()
    }
  }, [attachmentId, attachmentToken, hasBoundAttachment, messageApi, sessionId])

  const connect = async () => {
    if (sessionId == null) return
    const values = await form.validateFields()
    sessionStorage.setItem(credentialKey(sessionId), values.token)
    sessionStorage.setItem(attachmentKey(sessionId), values.attachmentId)
    setAttachmentToken(values.token)
    setAttachmentId(values.attachmentId)
  }

  const sendInput = useCallback((event: React.MouseEvent<HTMLVideoElement>, action: string) => {
    if (myRole === "observer") return
    const dc = dcRef.current
    if (dc == null || dc.readyState !== "open") return
    const rect = (event.target as HTMLElement).getBoundingClientRect()
    dc.send(JSON.stringify({ type: "touch", action, x: (event.clientX - rect.left) / rect.width, y: (event.clientY - rect.top) / rect.height }))
  }, [myRole])

  if (sessionId == null) return null
  if (attachmentToken == null || attachmentId == null) {
    return <Layout style={{ minHeight: "100vh" }}><Content style={{ maxWidth: 560, width: "100%", margin: "auto", padding: 24 }}>
      {contextHolder}
      <Title level={2}>Development session diagnostic</Title>
      <Paragraph>This route is for local development only. Supply the exact attachment ID and its attachment-scoped bearer credential. Administrator credentials are not accepted.</Paragraph>
      <Form form={form} layout="vertical" onFinish={() => void connect()}>
        <Form.Item name="attachmentId" label="Attachment ID" rules={[{ required: true, message: "Attachment ID is required" }]}><Input autoComplete="off" /></Form.Item>
        <Form.Item name="token" label="Attachment credential" rules={[{ required: true, message: "Attachment credential is required" }]}><Input.Password autoComplete="off" /></Form.Item>
        <Button htmlType="submit" type="primary">Connect diagnostic</Button>
      </Form>
    </Content></Layout>
  }

  return <Layout style={{ minHeight: "100vh" }}>
    {contextHolder}
    <Header style={{ display: "flex", alignItems: "center", gap: 12 }}><Title level={4} style={{ color: "#fff", margin: 0 }}>mako diagnostic</Title><Tag color={connected ? "success" : "default"}>{connected ? "live" : "connecting…"}</Tag><Tag color={ROLE_COLOR[myRole] ?? "default"}>{myRole}</Tag></Header>
    <Layout><Content style={{ background: "#000", display: "flex", alignItems: "center", justifyContent: "center" }}>
      <video ref={videoRef} autoPlay playsInline muted style={{ maxWidth: "100%", maxHeight: "calc(100vh - 64px)", cursor: myRole === "observer" ? "default" : "crosshair" }} onMouseDown={(event) => sendInput(event, "down")} onMouseUp={(event) => sendInput(event, "up")} onMouseMove={(event) => { if (event.buttons > 0) sendInput(event, "move") }} />
      {!connected && <div style={{ position: "absolute", color: "#fff" }}><Spin size="large" /></div>}
    </Content><Sider width={240} style={{ background: "#0f1923", padding: 16 }}><Text style={{ color: "#aaa", fontSize: 12 }}>ATTACHMENTS</Text><List size="small" dataSource={session?.attachments ?? []} renderItem={(attachment) => <List.Item style={{ padding: "4px 0", borderBottom: "none" }}><Space><Tag color={ROLE_COLOR[attachment.role] ?? "default"}>{attachment.role}</Tag><Text style={{ color: attachment.id === attachmentId ? "#4a9eff" : "#ccc", fontSize: 12 }}>{attachment.id === attachmentId ? "you" : attachment.clientId.slice(0, 8)}</Text></Space></List.Item>} /></Sider></Layout>
  </Layout>
}
