import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import Users from "./Users.js"

const { api } = vi.hoisted(() => ({
  api: {
    listUsers: vi.fn(),
    setUserType: vi.fn(),
    setBanStatus: vi.fn(),
    listUserCredentials: vi.fn(),
    revokeUserCredential: vi.fn(),
  },
}))

vi.mock("../api/client.js", () => ({ api }))

describe("Users", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.listUsers.mockResolvedValue({
      items: [{ id: "user-1", username: "ada", displayName: "Ada", status: "active", type: "registered" }],
      nextCursor: null,
    })
    api.setBanStatus.mockResolvedValue(undefined)
    api.setUserType.mockResolvedValue(undefined)
    api.listUserCredentials.mockResolvedValue([])
    api.revokeUserCredential.mockResolvedValue(undefined)
  })

  it("renders only safe user metadata and reports a completed ban", async () => {
    render(<Users />)

    await screen.findByText("Ada")
    expect(screen.getByText("ada")).toBeInTheDocument()
    expect(screen.queryByText(/token|hash/i)).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Ban" }))
    await waitFor(() => expect(api.setBanStatus).toHaveBeenCalledWith("user-1", "banned"))
    await screen.findByText("User banned")
  })

  it("surfaces an administrative mutation error", async () => {
    api.setBanStatus.mockRejectedValueOnce(new Error("cannot remove the final active admin"))
    render(<Users />)

    await screen.findByText("Ada")
    fireEvent.click(screen.getByRole("button", { name: "Ban" }))

    await screen.findByText("cannot remove the final active admin")
  })
})
