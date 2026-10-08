import { lazy, Suspense, useEffect, useState } from "react"
import { Alert, Button, Card, Layout, Menu, Spin, Typography } from "antd"
import { Link, Navigate, Route, Routes, useLocation } from "react-router-dom"
import { ApiError, api, type User } from "./api/client.js"
import { ADMIN_AUTHORIZATION_LOST_EVENT, ADMIN_SESSION_CLEARED_EVENT, adminSession } from "./auth/session.js"
import Audit from "./pages/Audit.js"
import Instances from "./pages/Instances.js"
import Invitations from "./pages/Invitations.js"
import Login from "./pages/Login.js"
import Setup from "./pages/Setup.js"
import Users from "./pages/Users.js"

const enableSessionDiagnostic = import.meta.env.VITE_MAKO_ENABLE_SESSION_DIAGNOSTIC === "true"
const SessionDiagnostic = enableSessionDiagnostic ? lazy(() => import("./pages/Session.js")) : null
const { Content, Header } = Layout
const { Paragraph, Title } = Typography

type Screen = "loading" | "setup" | "remote-setup" | "login" | "denied" | "admin" | "error" | "logout-unconfirmed"

export function isLocalSetupHost(hostname: string): boolean {
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "[::1]" || hostname === "::1"
}

function Centered({ children }: { children: React.ReactNode }) {
  return <Layout style={{ minHeight: "100vh" }}><Content style={{ display: "flex", alignItems: "center", justifyContent: "center", padding: 24 }}>{children}</Content></Layout>
}

function ProtectedShell({ onLogout }: { onLogout: () => Promise<void> }) {
  const [loggingOut, setLoggingOut] = useState(false)
  const location = useLocation()
  const logout = async () => { setLoggingOut(true); try { await onLogout() } finally { setLoggingOut(false) } }
  const selectedKey = location.pathname.startsWith("/invitations") ? "invitations" : location.pathname.startsWith("/instances") ? "instances" : location.pathname.startsWith("/audit") ? "audit" : "users"

  return <Layout style={{ minHeight: "100vh" }}>
    <Header style={{ display: "flex", alignItems: "center", gap: 20, paddingInline: 24 }}>
      <Title level={4} style={{ color: "#fff", margin: 0 }}>mako admin</Title>
      <Menu theme="dark" mode="horizontal" selectedKeys={[selectedKey]} style={{ flex: 1, minWidth: 0 }} items={[
        { key: "users", label: <Link to="/">Users</Link> },
        { key: "invitations", label: <Link to="/invitations">Invitations</Link> },
        { key: "instances", label: <Link to="/instances">Instances</Link> },
        { key: "audit", label: <Link to="/audit">Audit</Link> },
      ]} />
      <Button onClick={() => void logout()} loading={loggingOut}>Sign out</Button>
    </Header>
    <Content style={{ padding: 24 }}><Routes>
      <Route path="/" element={<Users />} />
      <Route path="/invitations" element={<Invitations />} />
      <Route path="/instances" element={<Instances />} />
      <Route path="/audit" element={<Audit />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes></Content>
  </Layout>
}

function AdminApp() {
  const [screen, setScreen] = useState<Screen>("loading")
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const resolve = async () => {
      setScreen("loading")
      setError(null)
      try {
        const { claimed } = await api.getAuthStatus()
        if (!claimed) {
          if (!cancelled) setScreen(isLocalSetupHost(window.location.hostname) ? "setup" : "remote-setup")
          return
        }
        if (adminSession.getToken() == null) {
          if (!cancelled) setScreen("login")
          return
        }
        const { user } = await api.getMe()
        if (!cancelled) setScreen(user.type === "admin" && user.status === "active" ? "admin" : "denied")
      } catch (cause) {
        if (cancelled) return
        if (cause instanceof ApiError && cause.status === 403) setScreen("denied")
        else if (adminSession.getToken() == null) setScreen("login")
        else { setError(cause instanceof Error ? cause.message : "Unable to verify administrator access"); setScreen("error") }
      }
    }
    const onCredentialCleared = () => { if (!cancelled) setScreen("login") }
    const onAuthorizationLost = () => { if (!cancelled) setScreen("denied") }
    window.addEventListener(ADMIN_SESSION_CLEARED_EVENT, onCredentialCleared)
    window.addEventListener(ADMIN_AUTHORIZATION_LOST_EVENT, onAuthorizationLost)
    void resolve()
    return () => {
      cancelled = true
      window.removeEventListener(ADMIN_SESSION_CLEARED_EVENT, onCredentialCleared)
      window.removeEventListener(ADMIN_AUTHORIZATION_LOST_EVENT, onAuthorizationLost)
    }
  }, [])

  const onAuthenticated = ({ user, token }: { user: User; token: string }) => {
    adminSession.setToken(token)
    setScreen(user.type === "admin" && user.status === "active" ? "admin" : "denied")
  }
  const logout = async () => {
    try { await api.logout(); adminSession.clear(); setScreen("login") }
    catch { const alreadyCleared = adminSession.getToken() == null; adminSession.clear(); setScreen(alreadyCleared ? "login" : "logout-unconfirmed") }
  }

  if (screen === "loading") return <Centered><Spin fullscreen tip="Checking administrator access…" /></Centered>
  if (screen === "setup") return <Centered><Setup onAuthenticated={onAuthenticated} /></Centered>
  if (screen === "remote-setup") return <Centered><Card style={{ width: 520, maxWidth: "100%" }}><Title level={2}>Administrator setup is local only</Title><Paragraph>To create the first administrator, open the WebUI on this server using localhost and enter the bootstrap secret from its local secret file.</Paragraph><Paragraph type="secondary">This hostname check only controls the setup form&apos;s presentation. The server independently verifies the local bootstrap secret.</Paragraph></Card></Centered>
  if (screen === "login") return <Centered><Login onAuthenticated={onAuthenticated} /></Centered>
  if (screen === "denied") return <Centered><Card style={{ width: 440, maxWidth: "100%" }}><Title level={2}>Access denied</Title><Paragraph>This account is valid but is not an active administrator.</Paragraph><Button onClick={() => adminSession.clear()}>Use a different account</Button></Card></Centered>
  if (screen === "logout-unconfirmed") return <Centered><Card style={{ width: 440, maxWidth: "100%" }}><Title level={2}>Signed out locally</Title><Alert type="warning" message="Server revocation could not be confirmed" description="This credential may still be valid on the server." showIcon /><Button style={{ marginTop: 16 }} onClick={() => setScreen("login")}>Continue to sign in</Button></Card></Centered>
  if (screen === "error") return <Centered><Card style={{ width: 440, maxWidth: "100%" }}><Alert type="error" message={error ?? "Unable to verify administrator access"} showIcon /></Card></Centered>
  return <ProtectedShell onLogout={logout} />
}

export default function App() {
  const location = useLocation()
  if (enableSessionDiagnostic && SessionDiagnostic != null && (location.pathname === "/session" || location.pathname.startsWith("/session/"))) {
    return <Suspense fallback={<Centered><Spin /></Centered>}><SessionDiagnostic /></Suspense>
  }
  return <AdminApp />
}
