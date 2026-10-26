# API surfaces

Two HTTP listeners, registered in `apps/server/cmd/mako-controlplane/main.go`. Route lists below were read directly from the route-registration code (`apiserver/{adminapi,clientapi,commonauth}`), not inferred from any design doc.

## Admin API — `127.0.0.1:3001`

Shared routes (via `commonauth.RegisterRoutes`, address-based throttling enabled):

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/auth/login` | |
| GET | `/api/v1/auth/me` | requires account-scoped credential |
| POST | `/api/v1/auth/logout` | requires account-scoped credential |

Admin-only routes:

| Method | Path | Notes |
|---|---|---|
| GET | `/api/v1/auth/status` | setup-claimed status, loopback-checked |
| POST | `/api/v1/auth/setup` | first-admin bootstrap, requires bootstrap secret |
| POST | `/api/v1/auth/registration-invites` | requires active admin; issues a token for `clientapi`'s `/auth/register` |
| GET | `/api/v1/admin/instances` | cross-owner list, requires active admin |
| POST | `/api/v1/admin/instances/{id}/start` | cross-owner, requires active admin |
| POST | `/api/v1/admin/instances/{id}/stop` | cross-owner, requires active admin |
| DELETE | `/api/v1/admin/instances/{id}` | cross-owner, requires active admin |
| GET | `/api/v1/admin/users` | cursor-paginated (`limit`, `cursor`), excludes guests, requires active admin |
| GET | `/api/v1/admin/users/{id}/credentials` | account-scoped credentials only, requires active admin |
| POST | `/api/v1/admin/users/{id}/credentials/{credentialId}/revoke` | requires active admin |
| POST | `/api/v1/admin/users/{id}/type` | promote/demote; refuses to demote the final active admin |
| POST | `/api/v1/admin/users/{id}/ban` | closes every session the user owns, revokes its other attachments; refuses to ban the final active admin |
| POST | `/api/v1/admin/users/{id}/unban` | |
| POST | `/api/v1/admin/sessions/{id}/attachments/{attachmentId}/revoke` | revokes one guest's attachment without closing the session |
| GET | `/api/v1/admin/audit` | cursor-paginated (`limit`, `cursor`), newest first, requires active admin |
| GET | `/api/v1/admin/images` | `?templateId=` required; lists every captured base image for that template, newest first — how an admin discovers candidate ids to promote |
| POST | `/api/v1/admin/images/{candidateId}/promote` | moves that candidate's template to point at it — the only base-image operation that changes what any instance resolves to; rollback is the same call with an older candidate's id |
| POST | `/api/v1/owner/access-codes` | **owner console, no bearer token**; body `{username?,ttlSeconds?}`; unbound when username omitted. Rejects external `Origin` and non-JSON POSTs |
| POST | `/api/v1/owner/users/reset-password` | **owner console, no bearer token**; `{username,password}`; revokes the user's account credentials |
| GET | `/healthz` | |

## Client API — `127.0.0.1:3000` (HTTPS, self-signed — see `security-model.md`)

Shared routes (via `commonauth.RegisterRoutes`, address-based throttling **disabled** — a proxied public-facing surface would otherwise see every client as one address and let a few failures block everyone):

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/auth/login` | |
| GET | `/api/v1/auth/me` | requires account-scoped credential |
| POST | `/api/v1/auth/logout` | requires account-scoped credential |

Client-only routes:

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/auth/register` | legacy self-registration via a registration-invite token; the Android client uses access codes instead |
| POST | `/api/v1/auth/credentials/{id}/revoke` | requires account-scoped credential, owner-checked |
| POST | `/api/v1/access-codes` | requires account-scoped credential. Ordinary user -> self-bound code; admin -> unbound code that can create a normal user or pair an existing account. Returns `code`, `expiresAt`, `scope` |
| POST | `/api/v1/access-codes/redeem` | **unauthenticated**. `{code,username,password,deviceLabel}`; creates a new ordinary account when the username is missing for an unbound code, otherwise verifies the password and pairs the device. Returns `{created,user,token,certSha256,serverId,serverName}` |
| POST | `/api/v1/auth/pairing-invites` | **tombstone** — returns `410 Gone` |
| POST | `/api/v1/auth/pairing-invites/redeem` | **tombstone** — returns `410 Gone` |
| POST | `/api/v1/instances` | requires account-scoped credential; owner becomes the caller; may be called repeatedly (multi-instance per owner) |
| GET | `/api/v1/instances` | owner-scoped list |
| GET | `/api/v1/instances/{id}` | owner-checked |
| GET | `/api/v1/templates` | requires account-scoped credential; lists the host's discovered AVDs (`templateId`/`displayName`/`runtimeClass`) for the client's template picker |
| POST | `/api/v1/instances/{id}/start` | owner-checked; spawns a dedicated `mako-worker` process for this instance and calls it to boot the emulator |
| POST | `/api/v1/instances/{id}/stop` | owner-checked |
| DELETE | `/api/v1/instances/{id}` | owner-checked |
| POST | `/api/v1/instances/{id}/capture-candidate-image` | owner-checked; instance must be stopped and have been started at least once. Snapshots its current AVD state into a new, immutable, *inert* `base_images` row — never affects what any other instance resolves to on its own (see `/admin/images/:candidateId/promote` above) |
| POST | `/api/v1/sessions` | requires account-scoped credential; creates the owner attachment + its credential |
| GET | `/api/v1/sessions/{id}` | account (owner-checked) or bound attachment credential; forbidden collapses to 404 |
| POST | `/api/v1/sessions/{id}/join` | **tombstone** — returns `410 Gone` |
| POST | `/api/v1/sessions/{id}/resume` | **tombstone** — returns `410 Gone` |
| POST | `/api/v1/sessions/{id}/attachments/{attachmentId}/resume` | requires attachment-scoped credential; rotates it |
| POST | `/api/v1/sessions/{id}/attachments/{attachmentId}/credentials/refresh` | requires attachment-scoped credential |
| POST | `/api/v1/sessions/{id}/attachments/{attachmentId}/disconnect` | account (owner) or the attachment itself |
| POST | `/api/v1/sessions/{id}/attachments/{attachmentId}/promote` | requires account-scoped owner credential; `TransitionAttachmentRole` to `participant` |
| POST | `/api/v1/sessions/{id}/attachments/{attachmentId}/demote` | requires account-scoped owner credential; `TransitionAttachmentRole` to `observer` |
| POST | `/api/v1/sessions/{id}/invites` | requires account-scoped owner credential; issues a 6-digit PIN, fixed 5-minute TTL |
| POST | `/api/v1/session-invites/redeem` | unauthenticated for `mode=guest`; bearer-authenticated for `mode=registered` — see `decisions-log.md` (2026-10-14) |
| POST | `/api/v1/signaling/session` | requires attachment-scoped credential bound to the named session/attachment; establishes transport then relays the SDP offer |
| POST | `/api/v1/signaling/ice-candidate` | requires attachment-scoped credential bound to the named session/attachment |
| GET | `/healthz` | |

## Not yet implemented (any surface)

No remaining pairing surface: unified access codes and certificate pinning are implemented (see `security-model.md`). Audio and multi-touch are implemented in the Go worker and Android client; the legacy registration-invite endpoints remain for compatibility but the Android client uses access codes.
