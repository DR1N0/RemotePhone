import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import Instances from "./Instances.js"

const { api } = vi.hoisted(() => ({
  api: { listAdminInstances: vi.fn(), startInstance: vi.fn(), stopInstance: vi.fn(), deleteInstance: vi.fn() },
}))
vi.mock("../api/client.js", () => ({ api }))

describe("Instances", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.listAdminInstances.mockResolvedValue([{ id: "instance-1", ownerId: "other-user", name: "Shared phone", runtimeClass: "android", templateId: "pixel", desiredState: "stopped", observedState: "stopped", lastError: null, createdAt: "2026-10-02", updatedAt: "2026-10-02" }])
    api.startInstance.mockResolvedValue({})
  })

  it("shows cross-owner safe metadata and can start an instance without a launch-session action", async () => {
    render(<Instances />)

    await screen.findByText("Shared phone")
    expect(screen.getByText("other-user")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /launch session/i })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Start" }))
    await waitFor(() => expect(api.startInstance).toHaveBeenCalledWith("instance-1"))
    await screen.findByText("Instance start requested")
  })
})
