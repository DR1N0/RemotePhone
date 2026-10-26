import "@testing-library/jest-dom/vitest"
import { cleanup } from "@testing-library/react"
import { afterEach, vi } from "vitest"

Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: vi.fn().mockImplementation(() => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
  })),
})

const jsdomGetComputedStyle = window.getComputedStyle.bind(window)
Object.defineProperty(window, "getComputedStyle", {
  writable: true,
  value: (element: Element) => jsdomGetComputedStyle(element),
})

afterEach(() => {
  cleanup()
  sessionStorage.clear()
})
