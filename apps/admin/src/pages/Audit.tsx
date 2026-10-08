import { useCallback, useEffect, useState } from "react"
import { Button, Table, Tag, Typography, message } from "antd"
import { api, type AuditEvent } from "../api/client.js"

const { Title } = Typography

export default function Audit() {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [messageApi, contextHolder] = message.useMessage()

  const load = useCallback(async (nextCursor: string | null, append: boolean) => {
    setLoading(true)
    try {
      const page = await api.listAudit({ limit: 20, cursor: nextCursor })
      setEvents((current) => append ? [...current, ...page.items.filter((item) => !current.some((known) => known.id === item.id))] : page.items)
      setCursor(page.nextCursor)
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Unable to load audit events")
    } finally {
      setLoading(false)
    }
  }, [messageApi])

  useEffect(() => { void load(null, false) }, [load])

  return (
    <section>
      {contextHolder}
      <Title level={2}>Audit</Title>
      <Table<AuditEvent>
        rowKey="id"
        loading={loading}
        dataSource={events}
        pagination={false}
        columns={[
          { title: "When", dataIndex: "createdAt", render: (value: string) => new Date(value).toLocaleString() },
          { title: "Action", dataIndex: "action" },
          { title: "Target", key: "target", render: (_, event) => `${event.targetType}: ${event.targetId}` },
          { title: "Outcome", dataIndex: "outcome", render: (outcome: AuditEvent["outcome"]) => <Tag color={outcome === "success" ? "success" : outcome === "failure" ? "error" : "processing"}>{outcome}</Tag> },
          { title: "Actor", dataIndex: "actorUserId", render: (actor: string | null) => actor ?? "system" },
        ]}
      />
      {cursor != null && <Button loading={loading} style={{ marginTop: 16 }} onClick={() => void load(cursor, true)}>Load more</Button>}
    </section>
  )
}
