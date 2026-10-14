# mako-server

Mako Server hosts Android runtime instances behind a control plane, reachable over a trusted LAN by a future native client. There is no browser-based streaming client.

This repository contains:
- a Go control plane + worker under active development in `apps/server` (two binaries, one module — `cmd/mako-controlplane` and `cmd/mako-worker`). Auth and instance CRUD/lifecycle are implemented; sessions/transport are in progress; admin, worker lifecycle, pairing, and base images are not yet ported. See [`docs/architecture/overview.md`](docs/architecture/overview.md) for current status.
- a legacy TypeScript control plane in `apps/controlplane`, being replaced by the above. Still the only end-to-end working flow today (instance creation, sessions, local attachment transport).

## Run locally (legacy TypeScript flow)

### Control plane prerequisites

```bash
cd apps/controlplane
npm install
```

### Start the worker

```bash
cd apps/server
go run ./cmd/mako-worker
```

By default the worker listens on `:7780` and reports its browser-facing base URL as `http://127.0.0.1:7780`.

### Start the control plane

```bash
cd apps/controlplane
npm run dev
```

By default the control plane listens on `http://127.0.0.1:3000` and talks to the worker at `http://127.0.0.1:7780`.

### Useful endpoints

- `GET /` — control-plane metadata
- `GET /healthz` — control-plane health
- `GET /api/v1/workers` — discovered worker inventory
- `GET /api/v1/templates` — discovered runtime templates
- `POST /api/v1/instances` — create a managed instance
- `POST /api/v1/sessions` — create a live session
- `POST /api/v1/signaling/session` — request prototype browser transport metadata
- `GET /openapi.json` — OpenAPI document
- `GET /docs` — Swagger UI

Open in a browser:
- http://127.0.0.1:3000/
- http://127.0.0.1:3000/docs

## Current local flow

1. Start the worker.
2. Start the control plane.
3. Discover workers and templates through the control-plane API.
4. Create a managed instance from a template.
5. Start the instance so the runtime handle and ports are persisted.
6. Create a session and attach a browser client.
7. Request `POST /api/v1/signaling/session` to receive prototype transport metadata.

The signaling path supports the local attachment transport flow. See [`docs/testing/local-transport-e2e.md`](docs/testing/local-transport-e2e.md) for the repeatable manual acceptance procedure.

## Transport limitations

The browser transport feature is validated only for the macOS Android Studio Emulator with Google Chrome on localhost or a controlled LAN. It does not provide public NAT traversal or TURN, authentication, or production reliability. These limitations make it unsuitable for public-network or untrusted-user use.
