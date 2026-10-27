# mako owner console

React/Vite UI embedded into `mako-controlplane` and served at
`http://127.0.0.1:3001/admin/` by default. The Go server and this dev
server both honor `MAKO_ADMIN_PORT`; the console host stays loopback-only.

- **Owner-only, no login.** Reaching the loopback listener is the owner proof.
  The UI calls `/api/v1/admin/*` without a bearer token; those requests are
  attributed to the hidden `_SYSTEM` principal.
- **Not a standalone deployment.** Vite emits `dist/` with base `/admin/`;
  `embed.go` includes it in the Go binary. `dist/` is committed so `go build`
  works without Node.
- **Rebuild after UI changes:**

  ```powershell
  cd apps/server/adminui
  npm ci
  npm run build
  ```

- **Dev server:** `npm run dev` proxies `/api` to `127.0.0.1:3001` by default;
  set `MAKO_ADMIN_PORT` when the Go server uses another port.

Pages: Overview, Users, Instances, Audit. Access codes and account removal
are actions in the Users page; server identity lives in Overview. Removing an
account stops and deletes its owned instances, revokes devices, and releases the
username; issue a new access code with Invite user to let the person recreate
the account.
