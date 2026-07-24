import { useState, useEffect, useCallback } from "react"
import { useNavigate } from "react-router-dom"
import {
  Table, Tag, Button, Space, Typography, Layout, message, Popconfirm,
} from "antd"
import { api, type Instance } from "../api/client.js"

const { Header, Content } = Layout
const { Title } = Typography

const STATE_COLOR: Record<string, string> = {
  running: "success",
  starting: "processing",
  stopping: "warning",
  stopped: "default",
  failed: "error",
}

export default function Dashboard() {
  const [instances, setInstances] = useState<Instance[]>([])
  const [loading, setLoading] = useState(true)
  const navigate = useNavigate()
  const [messageApi, contextHolder] = message.useMessage()

  const fetchInstances = useCallback(() => {
    api.listInstances()
      .then(setInstances)
      .catch(() => messageApi.error("Failed to fetch instances"))
      .finally(() => setLoading(false))
  }, [messageApi])

  useEffect(() => {
    fetchInstances()
    const timer = setInterval(fetchInstances, 3000)
    return () => clearInterval(timer)
  }, [fetchInstances])

  const handleStart = async (id: string) => {
    await api.startInstance(id).catch((e: Error) => messageApi.error(e.message))
    fetchInstances()
  }

  const handleStop = async (id: string) => {
    await api.stopInstance(id).catch((e: Error) => messageApi.error(e.message))
    fetchInstances()
  }

  const handleDelete = async (id: string) => {
    await api.deleteInstance(id).catch((e: Error) => messageApi.error(e.message))
    fetchInstances()
  }

  const handleLaunch = async (instanceId: string) => {
    try {
      const session = await api.createSession(instanceId)
      const ownerAttachment = session.attachments.find((a) => a.role === "owner")
      if (ownerAttachment == null) {
        throw new Error("no attachment returned from session creation")
      }
      sessionStorage.setItem(`attachment:${session.id}`, ownerAttachment.id)
      navigate(`/session/${session.id}`)
    } catch (e) {
      messageApi.error(e instanceof Error ? e.message : "Failed to create session")
    }
  }

  const columns = [
    {
      title: "Name",
      dataIndex: "name",
      key: "name",
    },
    {
      title: "State",
      key: "state",
      render: (_: unknown, record: Instance) => (
        <Tag color={STATE_COLOR[record.observedState] ?? "default"}>
          {record.observedState}
        </Tag>
      ),
    },
    {
      title: "Template",
      dataIndex: "templateId",
      key: "templateId",
    },
    {
      title: "Actions",
      key: "actions",
      render: (_: unknown, record: Instance) => (
        <Space>
          {record.observedState === "stopped" && (
            <Button size="small" type="primary" onClick={() => handleStart(record.id)}>
              Start
            </Button>
          )}
          {(record.observedState === "running" || record.observedState === "starting") && (
            <>
              <Button
                size="small"
                type="primary"
                onClick={() => handleLaunch(record.id)}
                disabled={record.observedState !== "running"}
              >
                Launch Session
              </Button>
              <Button size="small" danger onClick={() => handleStop(record.id)}>
                Stop
              </Button>
            </>
          )}
          {record.observedState === "stopped" && (
            <Popconfirm
              title="Delete this instance?"
              onConfirm={() => handleDelete(record.id)}
            >
              <Button size="small" danger>Delete</Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  return (
    <Layout style={{ minHeight: "100vh" }}>
      {contextHolder}
      <Header style={{ display: "flex", alignItems: "center", gap: 16 }}>
        <Title level={4} style={{ color: "#fff", margin: 0 }}>mako admin</Title>
      </Header>
      <Content style={{ padding: 24 }}>
        <Table
          rowKey="id"
          loading={loading}
          dataSource={instances}
          columns={columns}
          pagination={false}
        />
      </Content>
    </Layout>
  )
}
