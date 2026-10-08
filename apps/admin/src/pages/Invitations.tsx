import { useState } from "react"
import { Alert, Button, Card, Input, Space, Typography, message } from "antd"
import { api } from "../api/client.js"

const { Paragraph, Title } = Typography

export default function Invitations() {
  const [issued, setIssued] = useState<{ token: string; expiresAt: string } | null>(null)
  const [copied, setCopied] = useState(false)
  const [issuing, setIssuing] = useState(false)
  const [messageApi, contextHolder] = message.useMessage()

  const issue = async () => {
    setIssuing(true)
    try {
      setIssued(await api.createRegistrationInvite())
      setCopied(false)
      messageApi.success("Registration invitation issued")
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : "Unable to issue registration invitation")
    } finally {
      setIssuing(false)
    }
  }

  const copy = async () => {
    if (issued == null) return
    try {
      await navigator.clipboard.writeText(issued.token)
      setCopied(true)
      messageApi.success("Registration token copied")
    } catch {
      messageApi.error("Unable to copy registration token")
    }
  }

  return (
    <section>
      {contextHolder}
      <Title level={2}>Registration invitations</Title>
      <Card style={{ maxWidth: 640 }}>
        <Paragraph>Create a one-use registration invitation for a new account.</Paragraph>
        <Button type="primary" loading={issuing} onClick={() => void issue()}>Issue registration invitation</Button>
        {issued != null && (
          <Space direction="vertical" style={{ width: "100%", marginTop: 20 }}>
            <Alert type="warning" showIcon message="Copy this token now" description="It is shown only for this issuance and is not retained in this WebUI." />
            <Input aria-label="Registration token" value={issued.token} readOnly />
            <Paragraph type="secondary">Expires {new Date(issued.expiresAt).toLocaleString()}</Paragraph>
            <Button disabled={copied} onClick={() => void copy()}>{copied ? "Token copied" : "Copy token"}</Button>
          </Space>
        )}
      </Card>
    </section>
  )
}
