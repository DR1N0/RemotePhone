# LAN server discovery over mDNS

Status: **design**. The client currently requires a manually entered server URL
plus PIN pairing. This document specifies the discovery layer that will let the
server picker list nearby control planes automatically, without weakening the
PIN/certificate trust model.

## Service definition

- Service type: `_mako._tcp.local`
- One service per control plane, advertised on each **LAN** interface; not on
  Tailscale, Hyper-V, or other virtual interfaces.
- Instance name: `<display name> (<serverId first 8 chars>)` — the server ID
  suffix avoids collisions when two hosts use the same default name.
- SRV: target `<hostname>.local`, port = the control plane's client port
  (default 3000).
- TXT records:
  - `id=<serverId>` (stable UUID, required)
  - `name=<display name>` (may change)
  - `v=1` (discovery protocol version)
  - `path=/api/v1` (optional; future reverse-proxy support)
- TTL: 120 seconds; announce on startup, re-announce on address/name changes,
  and send goodbye records on shutdown.

The server ID and name come from `GET /api/v1/server/info`, which is
unauthenticated and already implemented. The ID is persisted next to the
database (`MAKO_SERVER_ID_PATH`, default `<db>.server-id`) and survives
restarts; `MAKO_SERVER_NAME` is the configurable display name.

## Client discovery

Android's `NsdManager` should be used instead of a hand-rolled multicast
socket: it handles multicast lock, interface changes, and battery behavior.
The flow:

1. On the server picker, start discovery for `_mako._tcp` and show a
   "Nearby servers" section alongside remembered servers.
2. Resolve each result to SRV/TXT/A (or AAAA) and read `id`, `name`, `v`.
3. Deduplicate by `id`, never by name or address.
4. For an already-paired server, update its `baseUrl` when the discovered
   address changes (DHCP), then revalidate with `GET /server/info`. The
   certificate pin is unchanged, so the profile keeps working.
5. For an unknown server, show it as "Not paired"; selecting it opens the
   normal PIN-pair + login flow.
6. If discovery and manual entry disagree, the server ID is authoritative;
   the address is only a hint.

Discovery must be best-effort: AP isolation, multicast filtering, VPN-only
networks, and sleep can all hide a server. Manual URL entry and remembered
servers remain the primary path.

## Security

mDNS is unauthenticated and spoofable. It is only an address hint:

- The client must fetch `/api/v1/server/info` and confirm the advertised
  `id` matches before reusing/updating a profile.
- TLS certificate pinning and PIN pairing remain mandatory; discovery never
  bypasses them.
- A rogue advertiser with a different ID appears as an unknown server and still
  requires the host's PIN.
- Advertise only on LAN interfaces. Tailscale/overlay clients use the
  configured/mDNS-free address.

## Server implementation options

- Add a small mDNS responder to `mako-controlplane` (a Go mDNS library or a
  minimal DNS packet responder) that publishes the TXT/SRV/A records above.
- Publish only while the client API listener is up; unpublish on shutdown.
- Re-publish when `MAKO_SERVER_NAME` or the LAN address changes.
- Keep the service out of the admin/worker surfaces.

## Test plan

- Same-LAN phone discovers a renamed/re-addressed control plane by server ID
  and reconnects with the existing certificate pin.
- Two servers with the same display name remain distinct (IDs differ).
- A spoofed advertisement with a wrong ID cannot masquerade as an existing
  server and still needs a valid PIN.
- Discovery failure falls back cleanly to manual URL entry.
