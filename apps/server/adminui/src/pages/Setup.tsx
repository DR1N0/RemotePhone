import { Alert, Button, Form, Input } from "antd"
import { useState } from "react"
import { api } from "../api/client.js"

type SetupValues = {
  username: string
  password: string
  bootstrapSecret: string
}

export default function Setup({ onComplete }: { onComplete: () => void }) {
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const submit = async (values: SetupValues) => {
    setError(null)
    setSubmitting(true)
    try {
      await api.setupAdmin({ ...values, deviceLabel: "remotephone-admin-web" })
      onComplete()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Administrator setup failed")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <>
      <div className="page-kicker">First run</div>
      <h1 className="page-title">Set up administrator</h1>
      <p className="page-subtitle">
        This creates the first admin account for the Android client. The owner
        console itself does not require a login.
      </p>
      {error != null && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
      <Form layout="vertical" onFinish={submit} requiredMark="optional">
        <Form.Item
          name="username"
          label="Admin username"
          rules={[
            { required: true },
            { pattern: /^[a-z0-9][a-z0-9_]{2,31}$/, message: "Start with a letter or digit; 3-32 chars" },
          ]}
        >
          <Input autoComplete="username" />
        </Form.Item>
        <Form.Item name="password" label="Password" rules={[{ required: true, min: 8 }]}>
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Form.Item name="bootstrapSecret" label="Bootstrap secret" rules={[{ required: true }]}>
          <Input.Password autoComplete="off" />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={submitting} block>
          Create admin
        </Button>
      </Form>
    </>
  )
}
