# mako owner console

React/Vite UI embedded into `mako-controlplane` and served at
`http://127.0.0.1:3001/admin/`.

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

- **Dev server:** `npm run dev` proxies `/api` to `127.0.0.1:3001`.

Pages: Overview, Users, Instances, Audit, Server. Access codes are issued from
the Users page (invite user or device code).
