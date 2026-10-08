import { useCallback, useEffect, useState } from "react"
import { Button, Drawer, message, Select, Space, Table, Tag, Typography } from "antd"
import { api, type AdminCredential, type User } from "../api/client.js"

const { Title, Text } = Typography

export default function Users() {
  const [users, setUsers] = useState<User[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [credentials, setCredentials] = useState<AdminCredential[] | null>(null)
  const [selectedUser, setSelectedUser] = useState<User | null>(null)
  const [messageApi, contextHolder] = message.useMessage()

  const load = useCallback(async (nextCursor: string | null, append: boolean) => {
    setLoading(true)
    try {
      const page = await api.listUsers({ limit: 20, cursor: nextCursor })
      setUsers((current) => append ? [...current, ...page.items.filter((item) => !current.some((known) => known.id === item.id))] : page.items)
      setCursor(page.nextCursor)
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Unable to load users")
    } finally {
      setLoading(false)
    }
  }, [messageApi])

  useEffect(() => { void load(null, false) }, [load])

  const mutate = async (action: () => Promise<void>, success: string) => {
    try {
      await action()
      messageApi.success(success)
      await load(null, false)
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "User update failed")
    }
  }

  const openCredentials = async (user: User) => {
    setSelectedUser(user)
    setCredentials(null)
    try {
      setCredentials(await api.listUserCredentials(user.id))
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Unable to load credentials")
      setCredentials([])
    }
  }

  return (
    <section>
      {contextHolder}
      <Title level={2}>Users</Title>
      <Table<User>
        rowKey="id"
        loading={loading}
        dataSource={users}
        pagination={false}
        columns={[
          { title: "Name", key: "name", render: (_, user) => <Space direction="vertical" size={0}><Text>{user.displayName}</Text><Text type="secondary">{user.username ?? "No username"}</Text></Space> },
          { title: "Type", dataIndex: "type", render: (type: User["type"]) => <Tag>{type}</Tag> },
          { title: "Status", dataIndex: "status", render: (status: User["status"]) => <Tag color={status === "active" ? "success" : "error"}>{status}</Tag> },
          {
            title: "Actions",
            key: "actions",
            render: (_, user) => <Space wrap>
              <Select
                aria-label={`Role for ${user.displayName}`}
                value={user.type === "guest" ? undefined : user.type}
                style={{ width: 120 }}
                disabled={user.type === "guest"}
                onChange={(type: "registered" | "admin") => void mutate(() => api.setUserType(user.id, type), "User type updated")}
                options={[{ value: "registered", label: "registered" }, { value: "admin", label: "admin" }]}
              />
              <Button size="small" onClick={() => void openCredentials(user)}>Credentials</Button>
              <Button
                size="small"
                danger={user.status === "active"}
                onClick={() => void mutate(() => api.setBanStatus(user.id, user.status === "active" ? "banned" : "active"), user.status === "active" ? "User banned" : "User unbanned")}
              >
                {user.status === "active" ? "Ban" : "Unban"}
              </Button>
            </Space>,
          },
        ]}
      />
      {cursor != null && <Button style={{ marginTop: 16 }} onClick={() => void load(cursor, true)}>Load more</Button>}
      <Drawer title={selectedUser == null ? "Credentials" : `Credentials — ${selectedUser.displayName}`} open={selectedUser != null} onClose={() => setSelectedUser(null)}>
        <Table<AdminCredential>
          rowKey="id"
          loading={credentials == null}
          dataSource={credentials ?? []}
          pagination={false}
          columns={[
            { title: "Device", dataIndex: "deviceLabel" },
            { title: "Expires", dataIndex: "expiresAt", render: (value: string | null) => value ?? "Never" },
            { title: "Status", dataIndex: "revokedAt", render: (value: string | null) => value == null ? <Tag color="success">active</Tag> : <Tag>revoked</Tag> },
            { title: "", key: "revoke", render: (_, credential) => selectedUser != null && credential.revokedAt == null ? <Button danger size="small" onClick={() => void mutate(async () => { await api.revokeUserCredential(selectedUser.id, credential.id); setCredentials(await api.listUserCredentials(selectedUser.id)) }, "Credential revoked")}>Revoke</Button> : null },
          ]}
        />
      </Drawer>
    </section>
  )
}
