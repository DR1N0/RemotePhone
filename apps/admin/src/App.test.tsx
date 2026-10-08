import { fireEvent, render, screen } from "@testing-library/react"
import { MemoryRouter } from "react-router-dom"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import App, { isLocalSetupHost } from "./App.js"
import { adminSession } from "./auth/session.js"

const { api, ApiError } = vi.hoisted(() => {
  class ApiError extends Error {
    constructor(public readonly status: number, message: string) {
      super(message)
    }
  }

  return {
    ApiError,
    api: {
      getAuthStatus: vi.fn(),
      getMe: vi.fn(),
      setupAdmin: vi.fn(),
      login: vi.fn(),
      logout: vi.fn(),
    },
  }
})

vi.mock("./api/client.js", () => ({ api, ApiError }))
vi.mock("./pages/Users.js", () => ({ default: () => <div>Users</div> }))
vi.mock("./pages/Invitations.js", () => ({ default: () => <div>Invitations</div> }))
vi.mock("./pages/Instances.js", () => ({ default: () => <div>Instances</div> }))
vi.mock("./pages/Audit.js", () => ({ default: () => <div>Audit</div> }))
vi.mock("./pages/Session.js", () => ({ default: () => <div>Session diagnostic</div> }))

const adminUser = {
  id: "admin-1",
  username: "root",
  displayName: "root",
  status: "active",
  type: "admin" as const,
}

function renderApp(path = "/") {
  return render(<MemoryRouter initialEntries={[path]}><App /></MemoryRouter>)
}

describe("App authentication gate", () => {
  beforeEach(() => {
    api.getAuthStatus.mockResolvedValue({ claimed: true })
    api.getMe.mockResolvedValue({ user: adminUser })
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  it("shows sign in without a credential once the server is claimed", async () => {
    renderApp()

    expect(screen.queryByText("Users")).not.toBeInTheDocument()
    await screen.findByText("Administrator sign in")
    expect(api.getAuthStatus).toHaveBeenCalledOnce()
  })

  it("shows local setup when the server is unclaimed", async () => {
    api.getAuthStatus.mockResolvedValue({ claimed: false })

    renderApp()

    await screen.findByText("Set up administrator")
    expect(api.getMe).not.toHaveBeenCalled()
  })

  it("limits setup presentation to loopback hostnames", () => {
    expect(isLocalSetupHost("localhost")).toBe(true)
    expect(isLocalSetupHost("127.0.0.1")).toBe(true)
    expect(isLocalSetupHost("[::1]")).toBe(true)
    expect(isLocalSetupHost("admin.example.test")).toBe(false)
  })

  it("does not render protected content while /auth/me is pending", async () => {
    adminSession.setToken("admin-token")
    api.getMe.mockReturnValue(new Promise(() => {}))

    renderApp()

    await screen.findByText("Checking administrator access…")
    expect(screen.queryByText("Users")).not.toBeInTheDocument()
  })

  it("renders protected routes for an active admin credential", async () => {
    adminSession.setToken("admin-token")

    renderApp()

    await screen.findAllByText("Users")
    expect(api.getMe).toHaveBeenCalledOnce()
  })

  it("denies a registered credential without revoking it", async () => {
    adminSession.setToken("registered-token")
    api.getMe.mockResolvedValue({ user: { ...adminUser, type: "registered" } })

    renderApp()

    await screen.findByText("Access denied")
    expect(api.logout).not.toHaveBeenCalled()
    expect(adminSession.getToken()).toBe("registered-token")
  })

  it("denies a valid guest attachment credential when /auth/me returns 403", async () => {
    adminSession.setToken("guest-attachment-token")
    api.getMe.mockRejectedValue(new ApiError(403, "account credential required"))

    renderApp()

    await screen.findByText("Access denied")
    expect(api.logout).not.toHaveBeenCalled()
    expect(adminSession.getToken()).toBe("guest-attachment-token")
  })

  it("unmounts protected UI after a live management authorization loss but retains the token", async () => {
    adminSession.setToken("demoted-token")
    renderApp()
    await screen.findAllByText("Users")

    window.dispatchEvent(new Event("mako:admin-authorization-lost"))

    await screen.findByText("Access denied")
    expect(screen.queryByText("Users")).not.toBeInTheDocument()
    expect(adminSession.getToken()).toBe("demoted-token")
  })

  it("returns to sign in for a 401-cleared credential", async () => {
    adminSession.setToken("expired-token")
    api.getMe.mockImplementation(async () => {
      adminSession.clear()
      throw new ApiError(401, "expired credential")
    })

    renderApp()

    await screen.findByText("Administrator sign in")
    expect(adminSession.getToken()).toBeNull()
  })

  it("clears local access and warns when logout revocation cannot be confirmed", async () => {
    adminSession.setToken("admin-token")
    api.logout.mockRejectedValue(new Error("network unavailable"))

    renderApp()
    await screen.findAllByText("Users")
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }))

    await screen.findByText("Server revocation could not be confirmed")
    expect(screen.queryByText("Users")).not.toBeInTheDocument()
    expect(adminSession.getToken()).toBeNull()
  })
})
