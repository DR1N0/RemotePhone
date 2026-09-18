import { useState, useEffect, useCallback } from "react"
import { useNavigate } from "react-router-dom"
import {
  Table, Tag, Button, Space, Typography, Layout, message, Popconfirm,
  Modal, Form, Input, InputNumber, Select, Switch,
} from "antd"
import { api, type Instance, type Template, type CreateInstanceRequest } from "../api/client.js"

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
  const [createOpen, setCreateOpen] = useState(false)
  const [templates, setTemplates] = useState<Template[]>([])
  const [creating, setCreating] = useState(false)
  const [form] = Form.useForm<CreateInstanceRequest>()
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

  const openCreate = () => {
    api.listTemplates()
      .then(setTemplates)
      .catch(() => messageApi.error("Failed to load templates"))
    form.resetFields()
    form.setFieldsValue({
      placement: { kind: "local-only" },
      resourceProfile: {
        cpuMillicores: 2000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "balanced",
      },
      launchConfig: { headless: true, readOnly: false, wipeData: false },
    })
    setCreateOpen(true)
  }

  const handleCreate = async () => {
    let values: CreateInstanceRequest
    try {
      values = await form.validateFields()
    } catch {
      return
    }
    setCreating(true)
    try {
      await api.createInstance(values)
      messageApi.success("Instance created")
      setCreateOpen(false)
      fetchInstances()
    } catch (e) {
      messageApi.error(e instanceof Error ? e.message : "Failed to create instance")
    } finally {
      setCreating(false)
    }
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
        <div style={{ marginBottom: 16, display: "flex", justifyContent: "flex-end" }}>
          <Button type="primary" onClick={openCreate}>Create Instance</Button>
        </div>
        <Table
          rowKey="id"
          loading={loading}
          dataSource={instances}
          columns={columns}
          pagination={false}
        />
      </Content>

      <Modal
        title="Create Instance"
        open={createOpen}
        onOk={handleCreate}
        onCancel={() => setCreateOpen(false)}
        okText="Create"
        confirmLoading={creating}
        width={520}
      >
        <Form form={form} layout="vertical" style={{ marginTop: 16 }}>
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input placeholder="my-instance" />
          </Form.Item>
          <Form.Item name="templateId" label="Template" rules={[{ required: true }]}>
            <Select
              placeholder="Select a template"
              options={templates.map((t) => ({ value: t.templateId, label: t.displayName || t.templateId }))}
            />
          </Form.Item>
          <Form.Item name={["placement", "kind"]} label="Placement">
            <Select disabled options={[{ value: "local-only", label: "Local only" }]} />
          </Form.Item>
          <Space style={{ width: "100%" }} size={12}>
            <Form.Item name={["resourceProfile", "cpuMillicores"]} label="CPU (millicores)" style={{ flex: 1 }}>
              <InputNumber min={1} style={{ width: "100%" }} />
            </Form.Item>
            <Form.Item name={["resourceProfile", "memoryMb"]} label="Memory (MB)" style={{ flex: 1 }}>
              <InputNumber min={1} style={{ width: "100%" }} />
            </Form.Item>
            <Form.Item name={["resourceProfile", "diskMb"]} label="Disk (MB)" style={{ flex: 1 }}>
              <InputNumber min={1} style={{ width: "100%" }} />
            </Form.Item>
          </Space>
          <Space style={{ width: "100%" }} size={12}>
            <Form.Item name={["resourceProfile", "accelerationMode"]} label="Acceleration" style={{ flex: 1 }}>
              <Select options={["forbid", "prefer", "require", "auto"].map((v) => ({ value: v, label: v }))} />
            </Form.Item>
            <Form.Item name={["resourceProfile", "streamProfile"]} label="Stream" style={{ flex: 1 }}>
              <Select options={["prototype", "balanced", "high"].map((v) => ({ value: v, label: v }))} />
            </Form.Item>
          </Space>
          <Space size={24}>
            <Form.Item name={["launchConfig", "headless"]} label="Headless" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name={["launchConfig", "readOnly"]} label="Read-only" valuePropName="checked">
              <Switch />
            </Form.Item>
            <Form.Item name={["launchConfig", "wipeData"]} label="Wipe data" valuePropName="checked">
              <Switch />
            </Form.Item>
          </Space>
        </Form>
      </Modal>
    </Layout>
  )
}
