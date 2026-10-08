const STORAGE_KEY = "mako.admin.account-token"
export const ADMIN_SESSION_CLEARED_EVENT = "mako:admin-session-cleared"
export const ADMIN_AUTHORIZATION_LOST_EVENT = "mako:admin-authorization-lost"

export const adminSession = {
  getToken(): string | null {
    return sessionStorage.getItem(STORAGE_KEY)
  },

  setToken(token: string): void {
    sessionStorage.setItem(STORAGE_KEY, token)
  },

  clear(): void {
    sessionStorage.removeItem(STORAGE_KEY)
    window.dispatchEvent(new Event(ADMIN_SESSION_CLEARED_EVENT))
  },
}
