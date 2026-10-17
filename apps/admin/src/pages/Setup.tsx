import { Alert, Button, Card, Form, Input, Typography } from "antd"
import { useState } from "react"
import { api, type SetupAdminRequest, type User } from "../api/client.js"

const { Title, Paragraph } = Typography

type SetupValues = Omit<SetupAdminRequest, "deviceLabel">

type SetupProps = {
  onAuthenticated: (result: { user: User; token: string }) => void
}

export default function Setup({ onAuthenticated }: SetupProps) {
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const submit = async (values: SetupValues) => {
    setError(null)
    setSubmitting(true)
    try {
      onAuthenticated(await api.setupAdmin({ ...values, deviceLabel: "mako-admin-web" }))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Administrator setup failed")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card style={{ width: 440, maxWidth: "100%" }}>
      <Title level={2}>Set up administrator</Title>
      <Paragraph>
        Enter the bootstrap secret from this server&apos;s local secret file to create its first administrator.
      </Paragraph>
      {error != null && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
      <Form layout="vertical" onFinish={submit} requiredMark="optional">
        <Form.Item name="username" label="Username" rules={[{ required: true, pattern: /^[a-z0-9_]{3,32}$/, message: "Use 3–32 lowercase letters, digits, or underscores" }]}>
          <Input autoComplete="username" />
        </Form.Item>
        <Form.Item name="password" label="Password" rules={[{ required: true, min: 8, message: "Use at least 8 characters" }]}>
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Form.Item name="bootstrapSecret" label="Bootstrap secret" rules={[{ required: true }]}>
          <Input.Password autoComplete="off" />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={submitting} block>Create administrator</Button>
      </Form>
    </Card>
  )
}
