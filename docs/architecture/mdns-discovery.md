# LAN server discovery over mDNS

Status: **implemented** for the control-plane advertisement and the Android
server picker. The client currently requires a manually entered server URL
plus PIN pairing when discovery finds nothing. This document specifies the
discovery layer and its security boundary; it never weakens the PIN/certificate
trust model.

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

Implemented with Android's `NsdManager` (a small Kotlin EventChannel, no
third-party mDNS dependency). The flow:

1. While the server picker is visible, browse `_mako._tcp`.
2. Resolve each result to SRV/TXT/A (or AAAA) and read `id`, `name`, `v`.
3. Deduplicate by `id`, never by name or address.
4. For an already-paired server, verify the advertised ID with
   `GET /server/info` over the pinned certificate before updating that
   profile's `baseUrl`/name. The certificate pin is unchanged.
5. For an unknown server, show it under "Nearby" with a **Pair** action;
   selecting it opens the normal PIN-pair + login flow.
6. If discovery and manual entry disagree, the server ID is authoritative;
   the address is only a hint.

Discovery is best-effort: AP isolation, multicast filtering, VPN-only
networks, and sleep can all hide a server. Manual URL entry and remembered
servers remain the primary path. Old service records may stay in Android's
cache until their TTL expires after an ungraceful server restart.

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

## Server implementation

`internal/controlplane/mdns` registers the service via
`github.com/grandcat/zeroconf` on private IPv4 LAN interfaces only
(Tailscale/link-local/loopback excluded). It publishes while the client API
listener is up and sends goodbye records on graceful shutdown. The advertiser
is disabled with `MAKO_DISABLE_MDNS=1`. A name/address change currently takes
effect on the next control-plane start; runtime re-announcement is future
work.

## Test plan

- Same-LAN phone discovers a renamed/re-addressed control plane by server ID
  and reconnects with the existing certificate pin.
- Two servers with the same display name remain distinct (IDs differ).
- A spoofed advertisement with a wrong ID cannot masquerade as an existing
  server and still needs a valid PIN.
- Discovery failure falls back cleanly to manual URL entry.
