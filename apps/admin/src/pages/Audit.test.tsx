import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import Audit from "./Audit.js"

const { api } = vi.hoisted(() => ({ api: { listAudit: vi.fn() } }))
vi.mock("../api/client.js", () => ({ api }))

describe("Audit", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.listAudit
      .mockResolvedValueOnce({ items: [{ id: "audit-1", action: "user.ban", targetType: "user", targetId: "user-1", outcome: "success", createdAt: "2026-10-02T00:00:00.000Z" }], nextCursor: "next" })
      .mockResolvedValueOnce({ items: [{ id: "audit-2", action: "instance.start", targetType: "instance", targetId: "instance-1", outcome: "success", createdAt: "2026-10-02T00:01:00.000Z" }], nextCursor: null })
  })

  it("advances the cursor and appends later audit events without duplicating the first page", async () => {
    render(<Audit />)

    await screen.findByText("user.ban")
    fireEvent.click(screen.getByRole("button", { name: "Load more" }))

    await screen.findByText("instance.start")
    expect(api.listAudit).toHaveBeenNthCalledWith(1, { limit: 20, cursor: null })
    expect(api.listAudit).toHaveBeenNthCalledWith(2, { limit: 20, cursor: "next" })
    expect(screen.getAllByText("user.ban")).toHaveLength(1)
  })
})
