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
| POST | `/api/v1/admin/instances/{id}/start` | cross-owner, requires active admin |
| POST | `/api/v1/admin/instances/{id}/stop` | cross-owner, requires active admin |
| DELETE | `/api/v1/admin/instances/{id}` | cross-owner, requires active admin |
| GET | `/healthz` | |

No `GET /api/v1/admin/instances` (list every instance across all owners) yet — that belongs to the `admin` package's own port, not yet built.

## Client API — `127.0.0.1:3000`

Shared routes (via `commonauth.RegisterRoutes`, address-based throttling **disabled** — a proxied public-facing surface would otherwise see every client as one address and let a few failures block everyone):

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/auth/login` | |
| GET | `/api/v1/auth/me` | requires account-scoped credential |
| POST | `/api/v1/auth/logout` | requires account-scoped credential |

Client-only routes:

| Method | Path | Notes |
|---|---|---|
| POST | `/api/v1/auth/register` | self-registration via a registration-invite token |
| POST | `/api/v1/auth/credentials/{id}/revoke` | requires account-scoped credential, owner-checked |
| POST | `/api/v1/auth/pairing-invites` | **tombstone** — returns `410 Gone` |
| POST | `/api/v1/auth/pairing-invites/redeem` | **tombstone** — returns `410 Gone` |
| POST | `/api/v1/instances` | requires account-scoped credential; owner becomes the caller |
| GET | `/api/v1/instances` | owner-scoped list |
| GET | `/api/v1/instances/{id}` | owner-checked |
| POST | `/api/v1/instances/{id}/start` | owner-checked; actually spawns/calls the configured worker |
| POST | `/api/v1/instances/{id}/stop` | owner-checked |
| DELETE | `/api/v1/instances/{id}` | owner-checked |
| GET | `/healthz` | |

## Not yet implemented (any surface)

Session/attachment lifecycle, signaling, admin cross-owner instance listing, audit log retrieval, worker inventory, template/base-image management, pairing. These route groups exist as empty stub packages (`session/`, `admin/`, `image/`, `pairing/`) with no HTTP routes registered anywhere yet.
