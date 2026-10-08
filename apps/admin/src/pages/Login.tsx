import { Alert, Button, Card, Form, Input, Typography } from "antd"
import { useState } from "react"
import { api, type LoginRequest, type User } from "../api/client.js"

const { Title } = Typography

type LoginValues = Omit<LoginRequest, "deviceLabel">

type LoginProps = {
  onAuthenticated: (result: { user: User; token: string }) => void
}

export default function Login({ onAuthenticated }: LoginProps) {
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const submit = async (values: LoginValues) => {
    setError(null)
    setSubmitting(true)
    try {
      onAuthenticated(await api.login({ ...values, deviceLabel: "mako-admin-web" }))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Sign in failed")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card style={{ width: 400, maxWidth: "100%" }}>
      <Title level={2}>Administrator sign in</Title>
      {error != null && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
      <Form layout="vertical" onFinish={submit} requiredMark="optional">
        <Form.Item name="username" label="Username" rules={[{ required: true }]}>
          <Input autoComplete="username" />
        </Form.Item>
        <Form.Item name="password" label="Password" rules={[{ required: true }]}>
          <Input.Password autoComplete="current-password" />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={submitting} block>Sign in</Button>
      </Form>
    </Card>
  )
}
