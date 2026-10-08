import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import Invitations from "./Invitations.js"

const { api } = vi.hoisted(() => ({ api: { createRegistrationInvite: vi.fn() } }))
vi.mock("../api/client.js", () => ({ api }))

describe("Invitations", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.createRegistrationInvite.mockResolvedValue({ token: "one-use-token", expiresAt: "2026-10-03T00:00:00.000Z" })
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  })

  it("shows and copies an issued registration token once without retaining it in a list", async () => {
    render(<Invitations />)

    fireEvent.click(screen.getByRole("button", { name: "Issue registration invitation" }))

    await screen.findByDisplayValue("one-use-token")
    fireEvent.click(screen.getByRole("button", { name: "Copy token" }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith("one-use-token"))
    expect(screen.getByRole("button", { name: "Token copied" })).toBeDisabled()
    expect(screen.queryByRole("table")).not.toBeInTheDocument()
  })
})
