import { useEffect, useState } from "react"
import { Alert, Spin } from "antd"
import { Navigate, NavLink, Route, Routes } from "react-router-dom"
import { api } from "./api/client.js"
import Audit from "./pages/Audit.js"
import Instances from "./pages/Instances.js"
import Overview from "./pages/Overview.js"
import Server from "./pages/Server.js"
import Setup from "./pages/Setup.js"
import Users from "./pages/Users.js"

type Screen = "loading" | "setup" | "ready" | "error"

function Brand() {
  return (
    <div className="admin-brand">
      <span className="admin-brand-name">mako</span>
      <span className="admin-brand-sub">owner console</span>
    </div>
  )
}

function Shell() {
  return (
    <div className="admin-shell">
      <header className="admin-header">
        <Brand />
        <nav className="admin-nav">
          <NavLink to="/" end>Overview</NavLink>
          <NavLink to="/users">Users</NavLink>
          <NavLink to="/instances">Instances</NavLink>
          <NavLink to="/audit">Audit</NavLink>
          <NavLink to="/server">Server</NavLink>
        </nav>
      </header>
      <main className="admin-content">
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/users" element={<Users />} />
          <Route path="/instances" element={<Instances />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/server" element={<Server />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}

export default function App() {
  const [screen, setScreen] = useState<Screen>("loading")
  const [error, setError] = useState<string | null>(null)

  const load = async () => {
    setScreen("loading")
    setError(null)
    try {
      const { claimed } = await api.getAuthStatus()
      setScreen(claimed ? "ready" : "setup")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Unable to reach the owner API")
      setScreen("error")
    }
  }

  useEffect(() => { void load() }, [])

  if (screen === "loading") {
    return <div className="centered-screen"><Spin size="large" /></div>
  }
  if (screen === "setup") {
    return (
      <div className="centered-screen">
        <div className="setup-card">
          <Brand />
          <div style={{ height: 28 }} />
          <Setup onComplete={() => setScreen("ready")} />
        </div>
      </div>
    )
  }
  if (screen === "error") {
    return (
      <div className="centered-screen">
        <div className="setup-card">
          <Alert
            type="error"
            showIcon
            message="Owner console unavailable"
            description={error ?? "The local admin API did not respond."}
          />
        </div>
      </div>
    )
  }
  return <Shell />
}
