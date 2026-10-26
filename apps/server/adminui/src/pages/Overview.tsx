import { useEffect, useState } from "react"
import { Card, Space, Spin, Tag, Typography, message } from "antd"
import { api, type AdminInstance, type AuditEvent, type ServerIdentity, type User } from "../api/client.js"

const { Text } = Typography

export default function Overview() {
  const [server, setServer] = useState<ServerIdentity | null>(null)
  const [instances, setInstances] = useState<AdminInstance[]>([])
  const [users, setUsers] = useState<User[]>([])
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [messageApi, contextHolder] = message.useMessage()

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      setLoading(true)
      try {
        const [serverResult, instanceResult, userResult, auditResult] = await Promise.all([
          api.getServer(),
          api.listAdminInstances(),
          api.listUsers({ limit: 100, cursor: null }),
          api.listAudit({ limit: 5, cursor: null }),
        ])
        if (cancelled) return
        setServer(serverResult)
        setInstances(instanceResult)
        setUsers(userResult.items)
        setEvents(auditResult.items)
      } catch (error) {
        messageApi.error(error instanceof Error ? error.message : "Unable to load overview")
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    void load()
    return () => { cancelled = true }
  }, [messageApi])

  if (loading) return <Spin />
  const running = instances.filter((instance) => instance.observedState === "running").length

  return (
    <section>
      {contextHolder}
      <div className="page-kicker">Server</div>
      <h1 className="page-title">{server?.serverName || "mako"}</h1>
      <p className="page-subtitle">
        Local owner console. No login is required; the loopback listener is the
        owner boundary.
      </p>
      <div className="stat-grid">
        <div className="stat-card">
          <div className="stat-label">Running instances</div>
          <div className="stat-value stat-accent">{running}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Instances</div>
          <div className="stat-value">{instances.length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Accounts</div>
          <div className="stat-value">{users.length}</div>
        </div>
        <div className="stat-card">
          <div className="stat-label">Access</div>
          <div className="stat-value"><Tag color="success">local owner</Tag></div>
        </div>
      </div>

      <div className="section-card">
        <Space direction="vertical" size={10} style={{ width: "100%" }}>
          <Text type="secondary">Server identity</Text>
          <CodeLine label="ID" value={server?.serverId ?? "—"} />
          <CodeLine label="Name" value={server?.serverName ?? "—"} />
          <CodeLine label="Certificate SHA-256" value={server?.certSha256 ?? "—"} />
        </Space>
      </div>

      <div className="section-card" style={{ marginTop: 12 }}>
        <Text type="secondary">Recent audit</Text>
        <div style={{ marginTop: 12 }}>
          {events.length === 0 && <Text type="secondary">No audit events yet.</Text>}
          {events.map((event) => (
            <div key={event.id} style={{ padding: "8px 0", borderBottom: "1px solid #1d1d1d" }}>
              <Space style={{ width: "100%", justifyContent: "space-between" }}>
                <Text>{event.action}</Text>
                <Text type="secondary">{new Date(event.createdAt).toLocaleString()}</Text>
              </Space>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

function CodeLine({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <Text type="secondary" style={{ fontSize: 11, display: "block" }}>{label}</Text>
      <span className="mono">{value}</span>
    </div>
  )
}
