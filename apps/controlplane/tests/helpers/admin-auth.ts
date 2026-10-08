import { mkdtempSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

export type RequestApp = {
  request: (input: string, init?: RequestInit) => Promise<Response>
}

export function createAdminAuthFixture(): { bootstrapSecretPath: string; cleanup: () => void } {
  const directory = mkdtempSync(join(tmpdir(), "mako-admin-auth-"))
  return {
    bootstrapSecretPath: join(directory, "bootstrap-secret"),
    cleanup: () => rmSync(directory, { recursive: true, force: true }),
  }
}

export async function setupAdmin(
  app: RequestApp,
  fixture: { bootstrapSecretPath: string },
  input: { username?: string; password?: string; deviceLabel?: string } = {},
): Promise<{ user: { id: string; type: string; username: string }; token: string }> {
  const response = await app.request("/api/v1/auth/setup", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      username: input.username ?? "admin",
      password: input.password ?? "long-password-for-test",
      deviceLabel: input.deviceLabel ?? "test-device",
      bootstrapSecret: readFileSync(fixture.bootstrapSecretPath, "utf8"),
    }),
  })
  if (response.status !== 201) {
    throw new Error(`admin setup failed with HTTP ${response.status}`)
  }
  return await response.json() as { user: { id: string; type: string; username: string }; token: string }
}
