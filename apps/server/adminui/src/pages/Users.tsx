import { useCallback, useEffect, useRef, useState } from "react"
import { Button, Drawer, Dropdown, Modal, Select, Space, Table, Tag, Typography, message } from "antd"
import { api, type AccessCode, type AdminCredential, type User } from "../api/client.js"

const { Text } = Typography

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const area = document.createElement("textarea")
    area.value = text
    area.setAttribute("readonly", "")
    area.style.position = "fixed"
    area.style.top = "0"
    area.style.left = "0"
    area.style.width = "1px"
    area.style.height = "1px"
    area.style.opacity = "0"
    document.body.appendChild(area)
    try {
      area.focus()
      area.select()
      area.setSelectionRange(0, text.length)
      return document.execCommand("copy")
    } finally {
      area.remove()
    }
  }
}

export default function Users() {
  const [users, setUsers] = useState<User[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [credentials, setCredentials] = useState<AdminCredential[] | null>(null)
  const [selectedUser, setSelectedUser] = useState<User | null>(null)
  const [issued, setIssued] = useState<{ code: AccessCode; target: string } | null>(null)
  const [issuing, setIssuing] = useState(false)
  const [deleteUser, setDeleteUser] = useState<User | null>(null)
  const [deleting, setDeleting] = useState(false)
  const codeRef = useRef<HTMLElement>(null)
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
    if (await copyText(issued.code.code)) {
      messageApi.success("Access code copied")
    } else if (codeRef.current != null) {
      const range = document.createRange()
      range.selectNodeContents(codeRef.current)
      const selection = window.getSelection()
      selection?.removeAllRanges()
      selection?.addRange(range)
      messageApi.info("Clipboard unavailable; code selected — press Ctrl+C")
    } else {
      messageApi.error("Unable to copy access code")
    }
  }

  const removeAccount = async () => {
    if (deleteUser == null) return
    setDeleting(true)
    try {
      await api.deleteUser(deleteUser.id)
      messageApi.success("Account removed; device credentials revoked")
      setDeleteUser(null)
      await load(null, false)
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Unable to remove account")
    } finally {
      setDeleting(false)
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
            title: "Username",
            dataIndex: "username",
            render: (username: string | null, user) => <Text>{username ?? user.displayName}</Text>,
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
                  aria-label={`Role for ${user.username ?? user.displayName}`}
                  value={user.type === "guest" || user.type === "system" ? undefined : user.type}
                  style={{ width: 118 }}
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
                  Access code
                </Button>
                <Button size="small" onClick={() => void openCredentials(user)}>Devices</Button>
                <Dropdown
                  trigger={["click"]}
                  menu={{
                    items: [
                      {
                        key: "ban",
                        label: user.status === "active" ? "Ban user" : "Unban user",
                        danger: user.status === "active",
                      },
                      { type: "divider" },
                      {
                        key: "remove",
                        label: "Remove account",
                        danger: true,
                        disabled: user.username == null,
                      },
                    ],
                    onClick: ({ key }) => {
                      if (key === "remove") {
                        setDeleteUser(user)
                      } else if (key === "ban") {
                        void mutate(
                          () => api.setBanStatus(user.id, user.status === "active" ? "banned" : "active"),
                          user.status === "active" ? "User banned" : "User unbanned",
                        )
                      }
                    },
                  }}
                >
                  <Button size="small">More</Button>
                </Dropdown>
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
        title={selectedUser == null ? "Devices" : `Devices for ${selectedUser.username ?? selectedUser.displayName}`}
        width={560}
        open={selectedUser != null}
        onClose={() => setSelectedUser(null)}
      >
        <p className="page-subtitle" style={{ marginBottom: 16 }}>
          Each row is an account credential issued when a device logged in.
          Revoking it invalidates that device&apos;s bearer token immediately;
          the user must sign in again.
        </p>
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
        width={520}
        title={issued?.target ? `Access code for ${issued.target}` : "Invite user"}
        onCancel={() => setIssued(null)}
        footer={[
          <Button key="close" onClick={() => setIssued(null)}>Close</Button>,
          <Button key="copy" type="primary" onClick={() => void copy()}>Copy</Button>,
        ]}
      >
        {issued != null && (
          <>
            <code ref={codeRef} className="code-display">{issued.code.code}</code>
            <p className="page-subtitle" style={{ marginBottom: 0 }}>
              {issued.target
                ? `Use this on the user's new device to add it to the ${issued.target} account.`
                : "Share this with someone who needs a new account, or with an existing user adding a device."}
              {" "}Expires {new Date(issued.code.expiresAt).toLocaleString()}.
            </p>
          </>
        )}
      </Modal>

      <Modal
        open={deleteUser != null}
        title={`Remove ${deleteUser?.username ?? "account"}?`}
        onCancel={() => setDeleteUser(null)}
        okText="Remove account"
        okButtonProps={{ danger: true }}
        confirmLoading={deleting}
        onOk={() => void removeAccount()}
      >
        <p className="page-subtitle">
          This deletes the account and revokes every device credential. The
          username is released. Use Invite user afterwards to create a fresh
          access code; owned instances are not deleted automatically.
        </p>
      </Modal>
    </section>
  )
}
