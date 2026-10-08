# Control-plane deployment

The control plane's raw HTTP listener is intentionally bound to `127.0.0.1`. Do not expose its port directly on a LAN or the public Internet.

Put it behind a TLS-terminating reverse proxy that is itself restricted to trusted operators, or use a protected authenticated tunnel. The proxy or tunnel is the public HTTPS endpoint; it forwards only to `http://127.0.0.1:<MAKO_CONTROLPLANE_PORT>` on the same machine. Preserve the normal `Authorization` header and do not treat forwarded client-address headers as an authentication mechanism.

**The proxy/tunnel MUST deny `/api/v1/auth/setup` and MUST NOT forward it upstream**, including when it runs on the same host. For example, return 404/403 at the public proxy location for that exact path. The backend also accepts setup only from a loopback peer with `Host: localhost`, `127.0.0.1`, or `[::1]`; this is defense in depth, not a replacement for proxy denial. A same-host proxy can originate a loopback connection and can rewrite its upstream Host, so backend code alone cannot reliably distinguish it from a direct local caller.

## Initial setup

1. Start the service locally and obtain the bootstrap secret from the machine-local secret file.
2. On the **server itself**, call `GET /api/v1/auth/status` at `http://127.0.0.1:<MAKO_CONTROLPLANE_PORT>`.
3. Only while `claimed` is false, use that same direct local address to call `POST /api/v1/auth/setup` with the bootstrap secret, an administrator username, password, and device label. Do not use the public HTTPS endpoint or a tunnel for this request.
4. The setup route denies every later setup request after the first administrator exists. It is not a remote account-recovery mechanism. After setup, remote HTTPS login is permitted through the protected proxy/tunnel.

If an administrator loses access, from the repository root use `npm --prefix apps/controlplane exec -- tsx apps/controlplane/src/cli/recover-admin.ts` at an interactive terminal on the host. Set `MAKO_CONTROLPLANE_DB_PATH` only to the intended locally owned, non-group/world-writable SQLite file; never point recovery at a copied or shared production database. The command selects an existing active administrator by stable User ID (or username when present), requests a new username and hidden password, and revokes that administrator's existing credentials in the same transaction. It cannot create an administrator and has no HTTP endpoint. A banned administrator must be unbanned by another administrator before local recovery is allowed.

## Database upgrade and owner remediation

Migration `0008_users_final` renames the physical `principals` table to `users` and the credential, attachment, and invite foreign-key columns to user names without changing IDs, token hashes, or history. On startup the service inspects legacy instance owners. If duplicate or invalid owners exist, it logs the exact instance IDs and continues running so an administrator can remediate them. It deliberately does not delete or rewrite those rows. Once remediation leaves no conflicts, startup installs the `instances_unique_owner` index; service-level immediate transactions protect new instance creation both before and after that index exists.

## Deferred scope

Native-client UI, app-idle policy, shared image, and app-catalog implementation are not part of this deployment.
