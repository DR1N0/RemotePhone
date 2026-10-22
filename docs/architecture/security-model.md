# Security model

## Implemented today

**Password hashing** (`apps/server/internal/controlplane/auth/password.go`): `scrypt`, N=16384, r=8, p=1, 64-byte derived key, 16-byte random salt, encoded as `scrypt-v1$16384$8$1$64$<salt-b64url>$<key-b64url>`. Verification parses the stored parameters and rejects any parameter *greater* than today's constants (a ceiling check — guards against a future lowering of the constants silently trusting a higher embedded value from an old hash). Comparison is `crypto/subtle.ConstantTimeCompare`, timing-safe.

**Bearer tokens** (`auth/token.go`): 32 cryptographically random bytes, base64url-encoded without padding (43 chars, 256 bits of entropy). Looked up by plain (unsalted) SHA-256 hex digest — unsalted deliberately, since the input is already high-entropy random data, not a low-entropy secret a dictionary attack could target.

**Bootstrap secret** (`auth/bootstrapsecret.go` + platform files): exactly 32 random bytes, base64url-encoded (43 chars). On Unix the file is created with mode exactly `0600` in a `0700` directory and read through `O_NOFOLLOW` with an exact-mode check; on Windows the file inherits the per-user NTFS ACL of its directory and a reparse point/symlink at the path is rejected before reading (POSIX mode bits are meaningless there). Writes go through a temp file + `os.Link`, so the published name never exposes a partially written secret. Used once, to authorize `POST /api/v1/auth/setup` (first-admin bootstrap).

**Authorization layering** (`apiserver/middleware/auth.go`): two distinct middleware layers, not one combined check —
- `RequireAuth` — missing/invalid/expired/revoked credential or banned user all collapse to a generic 401 (anti-enumeration: an attacker can't distinguish "wrong token" from "banned account" from "token for a scope that doesn't apply here").
- `RequireScope` — valid credential, but wrong scope or insufficient authority (e.g. not an admin) → 403.

**Registration invites**: admin-issued, single-use, TTL-bounded tokens (`registration_invites` table) required to self-register a new account via `POST /api/v1/auth/register`. The old TS-era "pairing invite" endpoints (`POST /api/v1/auth/pairing-invites`, `.../redeem`) are kept as tombstones on `clientapi` that return `410 Gone` — not removed outright, so any stale TS-era client gets an explicit "this is retired" response instead of a silent 404.

**TLS identity** (`internal/controlplane/tlsidentity`): the client surface generates a self-signed ECDSA P-256 certificate/key pair on first run and reuses it on every run after (`tls.LoadX509KeyPair`, regenerating only if either file is missing) — private key `0600`, matching every other on-disk secret's file-mode convention in this codebase (see `auth.BootstrapSecret`). 10-year validity; clients pin the certificate's SHA-256 after pairing, so an unexpected regeneration requires re-pairing. This is HTTPS trust bootstrap plus PIN pairing (below).

## Current network reality

- **Client API** (`127.0.0.1:3000` by default, `MAKO_CLIENT_ADDR=0.0.0.0:3000` for LAN) serves **HTTPS** with the self-signed certificate above. A new Android client must PIN-pair before login: the control plane prints a six-digit pairing PIN at startup, `POST /api/v1/pairing/verify` checks it (constant-time, five failures/minute), and the client stores the server certificate SHA-256 in secure storage. Every later connection pins that fingerprint; a certificate change rejects the connection. The first pairing itself is still TOFU over TLS, not a PAKE, so an active MITM on the very first pairing could substitute a certificate — use a trusted LAN for pairing.
- **Admin API** (`127.0.0.1:3001`) is unchanged: **plain HTTP, loopback-bound**. Its loopback binding is its own independent security barrier (a stolen bearer token or missed authorization check still can't be exploited without actual host access) — TLS doesn't change that, and the roadmap's "HTTPS for the client surface" was always scoped to the client surface only.

## Implemented: PIN-gated certificate pinning

The client API's self-signed certificate is trusted through a PIN-gated first contact:

1. Control plane generates a six-digit PIN at startup (or uses `MAKO_PAIRING_PIN`) and logs it together with the certificate SHA-256.
2. The client connects with a temporary unpinned HTTP client and submits the PIN to `POST /api/v1/pairing/verify`.
3. The server verifies the PIN (constant-time; five failures/minute) and returns the certificate SHA-256; the client checks that it matches the certificate actually presented on this TLS connection, then stores it via `flutter_secure_storage`.
4. All later clients pin that certificate hash; a different certificate makes the TLS handshake fail.

This is PIN-gated TOFU, not a PAKE: an active MITM during the very first pairing could still substitute its certificate. It does remove blind trust-on-first-use and passive interception; a future revision can add a PAKE/SAS for first-contact resistance. Authorization after pairing remains bearer-token based — pairing only establishes TLS trust, not user identity.

The control plane PIN is regenerated on each restart unless `MAKO_PAIRING_PIN` is set; already-paired clients keep working (their pinned fingerprint is unchanged). Rotating the server certificate requires re-pairing.

## Networking target (designed, not yet built)

Three distinct traffic paths are planned:

1. **Control-plane client API** — HTTPS with the self-signed certificate, PIN-paired and certificate-pinned; LAN-reachable via `MAKO_CLIENT_ADDR`.
2. **Worker media/data ports** — the worker's pion WebRTC stack is IPv4/UDP only and advertises every routable IPv4 interface (LAN, Tailscale, other VPNs; link-local/multicast/IPv6 excluded, loopback kept for local probes), letting ICE choose. This is deliberate: hard-coding one LAN address would make Tailscale/VPN clients unreachable, and ICE exists to select among interfaces. `MAKO_WEBRTC_HOST_IP` is available as a debug override, not the normal path. There is still no ephemeral UDP port range. Only the control plane's client API is authenticated by bearer tokens; the media channel is protected by SDP/ICE secrecy, not application auth.
3. **Worker internal control API** — strictly `127.0.0.1`-bound, never LAN-reachable; unaffected by this phase.
