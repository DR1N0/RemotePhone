import { useCallback, useEffect, useState } from "react"
import { Button, Popconfirm, Space, Table, Tag, Typography, message } from "antd"
import { api, type AdminInstance, type User } from "../api/client.js"

const { Text } = Typography
const STATE_COLOR: Record<string, string> = {
  running: "success",
  starting: "processing",
  stopping: "warning",
  stopped: "default",
  failed: "error",
}

export default function Instances() {
  const [instances, setInstances] = useState<AdminInstance[]>([])
  const [owners, setOwners] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [messageApi, contextHolder] = message.useMessage()

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [items, page] = await Promise.all([
        api.listAdminInstances(),
        api.listUsers({ limit: 100, cursor: null }),
      ])
      setInstances(items)
      setOwners(Object.fromEntries(
        page.items.map((user: User) => [user.id, user.username ?? user.displayName]),
      ))
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Unable to load instances")
    } finally {
      setLoading(false)
    }
  }, [messageApi])

  useEffect(() => { void load() }, [load])

  const mutate = async (action: () => Promise<unknown>, success: string) => {
    try {
      await action()
      messageApi.success(success)
      await load()
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Instance update failed")
    }
  }

  return (
    <section>
      {contextHolder}
      <div className="page-kicker">Runtime</div>
      <h1 className="page-title">Instances</h1>
      <p className="page-subtitle">
        Every instance has one worker and one cloned AVD. Start, stop, or delete
        them independently.
      </p>
      <Button style={{ marginBottom: 16 }} onClick={() => void load()}>Refresh</Button>
      <Table<AdminInstance>
        rowKey="id"
        loading={loading}
        dataSource={instances}
        pagination={false}
        columns={[
          { title: "Name", dataIndex: "name" },
          {
            title: "Owner",
            dataIndex: "ownerId",
            render: (ownerId: string) => owners[ownerId] ?? <Text code>{ownerId}</Text>,
          },
          {
            title: "State",
            key: "state",
            render: (_, instance) => (
              <Tag color={STATE_COLOR[instance.observedState] ?? "default"}>{instance.observedState}</Tag>
            ),
          },
          { title: "Template", dataIndex: "templateId" },
          { title: "Last error", dataIndex: "lastError", render: (value: string | null) => value ?? "—" },
          {
            title: "Actions",
            key: "actions",
            render: (_, instance) => (
              <Space>
                {instance.observedState === "stopped" && (
                  <Button size="small" type="primary" onClick={() => void mutate(() => api.startInstance(instance.id), "Instance start requested")}>
                    Start
                  </Button>
                )}
                {(instance.observedState === "running" || instance.observedState === "starting") && (
                  <Button size="small" danger onClick={() => void mutate(() => api.stopInstance(instance.id), "Instance stop requested")}>
                    Stop
                  </Button>
                )}
                {instance.observedState === "stopped" && (
                  <Popconfirm title="Delete this instance?" onConfirm={() => void mutate(() => api.deleteInstance(instance.id), "Instance deleted")}>
                    <Button size="small" danger>Delete</Button>
                  </Popconfirm>
                )}
              </Space>
            ),
          },
        ]}
      />
    </section>
  )
}
