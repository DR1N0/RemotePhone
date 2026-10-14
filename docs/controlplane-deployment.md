# Control-plane deployment

> **Legacy.** This describes the TypeScript control plane (`apps/controlplane`), which is being retired in favor of a Go implementation at `apps/server`. The Go control plane has no deployment guide yet — it doesn't yet implement HTTPS, PIN-pairing, or most of the API surface described below. See `docs/architecture/overview.md` for current Go-port status.

One Control Plane process runs two **loopback-only** HTTP listeners. Neither raw listener may be exposed on a LAN or the public Internet.

| Surface | Listener | Purpose | Public exposure |
| --- | --- | --- | --- |
| Client API | `127.0.0.1:${MAKO_CONTROLPLANE_PORT}` (default `3000`) | Native-client account, Instance, Session, invite, and signaling API | HTTPS reverse proxy on external `443` only |
| Management | `127.0.0.1:${MAKO_ADMIN_PORT}` (default `3001`) | WebUI, first-admin bootstrap/status, local administration, Worker inventory, and docs | Server host only; never a proxy upstream |

`MAKO_CONTROLPLANE_PORT` remains the client-API port for compatibility with the existing proxy upstream. `MAKO_ADMIN_PORT` defaults to `3001`; the ports must be distinct. No TLS listener runs in Node: the separately configured public proxy owns external `443` and forwards only to `http://127.0.0.1:3000`.

## Public HTTPS proxy boundary

The public proxy terminates TLS and preserves the normal `Authorization` header. It must not use forwarded client-address headers as an authentication mechanism. The Control Plane does not trust arbitrary `X-Forwarded-For`; use proxy-level source rate limits when required.

At external `443`, forward only client routes to `127.0.0.1:3000` and reject management paths as defense in depth. The client Hono app does not register these paths and therefore returns JSON `404`, but the proxy deny rules prevent accidental future exposure:

| Public path | Proxy action |
| --- | --- |
| `/admin` and `/admin/*` | deny; never forward to `3001` |
| `/api/v1/admin/*` | deny |
| `/api/v1/auth/setup` | deny |
| `/docs` and `/openapi.json` | deny |
| Other client API paths | forward to `http://127.0.0.1:3000` only |

For example, an Nginx-style route order is:

```nginx
# TLS server listening on :443; certificate directives are deployment-managed.
location = /api/v1/auth/setup { return 404; }
location ^~ /api/v1/admin/ { return 404; }
location = /docs { return 404; }
location = /openapi.json { return 404; }
location = /admin { return 404; }
location ^~ /admin/ { return 404; }
location / { proxy_pass http://127.0.0.1:3000; }
```

Do not add a `3001` upstream, do not forward management traffic through the public proxy, and do not publish `3000` or `3001` directly. This guide does not create certificates or reconfigure a live proxy.

## Local management WebUI and initial setup

On the server host, build/start the service and open:

```text
http://127.0.0.1:3001/admin
```

Replace `3001` with `MAKO_ADMIN_PORT` when configured. The management app serves setup, status, WebUI, local admin APIs, Worker inventory, and documentation. The old `http://127.0.0.1:3000/admin` address deliberately returns JSON `404`; it does not redirect to port `3001`.

1. Obtain the first-admin bootstrap secret from its owner-only local secret file.
2. On the server itself, inspect `GET http://127.0.0.1:3001/api/v1/auth/status`.
3. While `claimed` is false, use the local WebUI or `POST http://127.0.0.1:3001/api/v1/auth/setup` with the bootstrap secret, administrator username, password, and device label.
4. Setup is rejected once an administrator exists. It is not remote account recovery; do not use public HTTPS or a tunnel for it.

An `admin` account token remains a normal account credential. At client HTTPS/`3000`, it can discover and manage only Instances it owns. On the local management app, the current active admin type is checked for user management, audit, and cross-owner Instance start/stop/delete. The same token is accepted by both local surfaces; no SQL migration, credential rotation, re-claim, or database reset is required for the port split.

If an administrator loses access, from the repository root use `npm --prefix apps/controlplane exec -- tsx apps/controlplane/src/cli/recover-admin.ts` at an interactive terminal on the host. Set `MAKO_CONTROLPLANE_DB_PATH` only to the intended locally owned, non-group/world-writable SQLite file; never point recovery at a copied or shared production database. The command selects an existing active administrator by stable User ID (or username when present), requests a new username and hidden password, and revokes that administrator's existing credentials in the same transaction. It cannot create an administrator and has no HTTP endpoint. A banned administrator must be unbanned by another administrator before local recovery is allowed.

## Database upgrade and owner remediation

Migration `0008_users_final` renames the physical `principals` table to `users` and the credential, attachment, and invite foreign-key columns to user names without changing IDs, token hashes, or history. On startup the service inspects legacy instance owners. If duplicate or invalid owners exist, it logs the exact instance IDs and continues running so an administrator can remediate them. It deliberately does not delete or rewrite those rows. Once remediation leaves no conflicts, startup installs the `instances_unique_owner` index; service-level immediate transactions protect new instance creation both before and after that index exists.

## Deferred scope

Native-client UI, app-idle policy, shared image, and app-catalog implementation are not part of this deployment.
