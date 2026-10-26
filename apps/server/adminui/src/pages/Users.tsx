import { useCallback, useEffect, useState } from "react"
import { Button, Drawer, Modal, Select, Space, Table, Tag, Typography, message } from "antd"
import { api, type AccessCode, type AdminCredential, type User } from "../api/client.js"

const { Text } = Typography

export default function Users() {
  const [users, setUsers] = useState<User[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [credentials, setCredentials] = useState<AdminCredential[] | null>(null)
  const [selectedUser, setSelectedUser] = useState<User | null>(null)
  const [issued, setIssued] = useState<{ code: AccessCode; target: string } | null>(null)
  const [issuing, setIssuing] = useState(false)
  const [messageApi, contextHolder] = message.useMessage()

  const load = useCallback(async (nextCursor: string | null, append: boolean) => {
    setLoading(true)
    try {
      const page = await api.listUsers({ limit: 20, cursor: nextCursor })
      setUsers((current) => append
        ? [...current, ...page.items.filter((item) => !current.some((known) => known.id === item.id))]
        : page.items)
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

  const issue = async (target: User | null) => {
    setIssuing(true)
    try {
      const code = await api.createAccessCode(target?.username ? { username: target.username } : {})
      setIssued({ code, target: target?.username ?? "" })
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Unable to create access code")
    } finally {
      setIssuing(false)
    }
  }

  const copy = async () => {
    if (issued == null) return
    try {
      await navigator.clipboard.writeText(issued.code.code)
      messageApi.success("Access code copied")
    } catch {
      messageApi.error("Unable to copy access code")
    }
  }

  return (
    <section>
      {contextHolder}
      <div className="page-kicker">Identity</div>
      <h1 className="page-title">Users</h1>
      <p className="page-subtitle">
        Accounts, roles, devices, and access codes. An access code can create a
        new ordinary user or add a device to an existing account.
      </p>
      <Space style={{ marginBottom: 16 }}>
        <Button type="primary" loading={issuing} onClick={() => void issue(null)}>
          Invite user
        </Button>
        <Button onClick={() => void load(null, false)}>Refresh</Button>
      </Space>
      <Table<User>
        rowKey="id"
        loading={loading}
        dataSource={users}
        pagination={false}
        columns={[
          {
            title: "Account",
            key: "account",
            render: (_, user) => (
              <Space direction="vertical" size={0}>
                <Text>{user.displayName}</Text>
                <Text type="secondary">{user.username ?? "No username"}</Text>
              </Space>
            ),
          },
          { title: "Type", dataIndex: "type", render: (type: User["type"]) => <Tag>{type}</Tag> },
          {
            title: "Status",
            dataIndex: "status",
            render: (status: User["status"]) => (
              <Tag color={status === "active" ? "success" : "error"}>{status}</Tag>
            ),
          },
          {
            title: "Actions",
            key: "actions",
            render: (_, user) => (
              <Space wrap>
                <Select
                  aria-label={`Role for ${user.displayName}`}
                  value={user.type === "guest" || user.type === "system" ? undefined : user.type}
                  style={{ width: 124 }}
                  disabled={user.type === "guest" || user.type === "system"}
                  onChange={(type: "registered" | "admin") =>
                    void mutate(() => api.setUserType(user.id, type), "User type updated")}
                  options={[
                    { value: "registered", label: "registered" },
                    { value: "admin", label: "admin" },
                  ]}
                />
                <Button
                  size="small"
                  disabled={!user.username}
                  loading={issuing}
                  onClick={() => void issue(user)}
                >
                  Device code
                </Button>
                <Button size="small" onClick={() => void openCredentials(user)}>Devices</Button>
                <Button
                  size="small"
                  danger={user.status === "active"}
                  onClick={() => void mutate(
                    () => api.setBanStatus(user.id, user.status === "active" ? "banned" : "active"),
                    user.status === "active" ? "User banned" : "User unbanned",
                  )}
                >
                  {user.status === "active" ? "Ban" : "Unban"}
                </Button>
              </Space>
            ),
          },
        ]}
      />
      {cursor != null && (
        <Button loading={loading} style={{ marginTop: 16 }} onClick={() => void load(cursor, true)}>
          Load more
        </Button>
      )}

      <Drawer
        title={selectedUser == null ? "Devices" : `Devices for ${selectedUser.displayName}`}
        width={560}
        open={selectedUser != null}
        onClose={() => setSelectedUser(null)}
      >
        {credentials == null && <Text type="secondary">Loading...</Text>}
        {credentials != null && credentials.length === 0 && <Text type="secondary">No account devices.</Text>}
        {credentials != null && credentials.map((credential) => (
          <div key={credential.id} className="section-card" style={{ marginBottom: 10 }}>
            <Space direction="vertical" size={4} style={{ width: "100%" }}>
              <Space style={{ justifyContent: "space-between", width: "100%" }}>
                <Text>{credential.deviceLabel}</Text>
                <Button
                  size="small"
                  danger
                  onClick={() => void mutate(
                    () => api.revokeUserCredential(selectedUser!.id, credential.id),
                    "Device credential revoked",
                  )}
                >
                  Revoke
                </Button>
              </Space>
              <Text type="secondary">Created {new Date(credential.createdAt).toLocaleString()}</Text>
            </Space>
          </div>
        ))}
      </Drawer>

      <Modal
        open={issued != null}
        title={issued?.target ? `Access code for ${issued.target}` : "Invite user"}
        onCancel={() => setIssued(null)}
        footer={[
          <Button key="close" onClick={() => setIssued(null)}>Close</Button>,
          <Button key="copy" type="primary" onClick={() => void copy()}>Copy</Button>,
        ]}
      >
        {issued != null && (
          <>
            <code className="code-display">{issued.code.code}</code>
            <p className="page-subtitle" style={{ marginBottom: 0 }}>
              {issued.target
                ? `Use this on the user's new device to add it to the ${issued.target} account.`
                : "Share this with someone who needs a new account, or with an existing user adding a device."}
              {" "}Expires {new Date(issued.code.expiresAt).toLocaleString()}.
            </p>
          </>
        )}
      </Modal>
    </section>
  )
}

