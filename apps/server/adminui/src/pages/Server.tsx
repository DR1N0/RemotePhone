import { useEffect, useState } from "react"
import { Alert, Button, Form, Input, Space, Typography, message } from "antd"
import { api, type ServerIdentity } from "../api/client.js"

const { Text } = Typography

export default function Server() {
  const [server, setServer] = useState<ServerIdentity | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [resetting, setResetting] = useState(false)
  const [messageApi, contextHolder] = message.useMessage()

  const load = async () => {
    try {
      setServer(await api.getServer())
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Unable to load server identity")
    }
  }

  useEffect(() => { void load() }, [])

  const reset = async (values: { username: string; password: string }) => {
    setResetting(true)
    try {
      await api.resetPassword(values)
      messageApi.success("Password reset; existing account credentials revoked")
    } catch (cause) {
      messageApi.error(cause instanceof Error ? cause.message : "Unable to reset password")
    } finally {
      setResetting(false)
    }
  }

  return (
    <section>
      {contextHolder}
      <div className="page-kicker">Owner</div>
      <h1 className="page-title">Server</h1>
      <p className="page-subtitle">
        Identity and recovery operations. This console is reachable only from
        the server itself or through an SSH tunnel.
      </p>
      {error != null && <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />}

      <div className="section-card">
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Text type="secondary">Server identity</Text>
          <div>
            <Text type="secondary" style={{ fontSize: 11 }}>Server ID</Text>
            <div className="mono">{server?.serverId ?? "—"}</div>
          </div>
          <div>
            <Text type="secondary" style={{ fontSize: 11 }}>Name</Text>
            <div>{server?.serverName ?? "—"}</div>
          </div>
          <div>
            <Text type="secondary" style={{ fontSize: 11 }}>Certificate SHA-256</Text>
            <div className="mono">{server?.certSha256 ?? "—"}</div>
          </div>
        </Space>
      </div>

      <div className="section-card" style={{ marginTop: 12 }}>
        <Text type="secondary">Reset account password</Text>
        <p className="page-subtitle" style={{ marginTop: 8 }}>
          Use this if an admin or user loses their password. It revokes every
          existing account credential for that user.
        </p>
        <Form layout="vertical" onFinish={reset} requiredMark="optional" style={{ maxWidth: 440 }}>
          <Form.Item name="username" label="Username" rules={[{ required: true }]}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label="New password" rules={[{ required: true, min: 8 }]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={resetting}>Reset password</Button>
        </Form>
      </div>
    </section>
  )
}
