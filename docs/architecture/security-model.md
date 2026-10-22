# Security model

## Implemented today

**Password hashing** (`apps/server/internal/controlplane/auth/password.go`): `scrypt`, N=16384, r=8, p=1, 64-byte derived key, 16-byte random salt, encoded as `scrypt-v1$16384$8$1$64$<salt-b64url>$<key-b64url>`. Verification parses the stored parameters and rejects any parameter *greater* than today's constants (a ceiling check — guards against a future lowering of the constants silently trusting a higher embedded value from an old hash). Comparison is `crypto/subtle.ConstantTimeCompare`, timing-safe.

**Bearer tokens** (`auth/token.go`): 32 cryptographically random bytes, base64url-encoded without padding (43 chars, 256 bits of entropy). Looked up by plain (unsalted) SHA-256 hex digest — unsalted deliberately, since the input is already high-entropy random data, not a low-entropy secret a dictionary attack could target.

**Bootstrap secret** (`auth/bootstrapsecret.go` + platform files): exactly 32 random bytes, base64url-encoded (43 chars). On Unix the file is created with mode exactly `0600` in a `0700` directory and read through `O_NOFOLLOW` with an exact-mode check; on Windows the file inherits the per-user NTFS ACL of its directory and a reparse point/symlink at the path is rejected before reading (POSIX mode bits are meaningless there). Writes go through a temp file + `os.Link`, so the published name never exposes a partially written secret. Used once, to authorize `POST /api/v1/auth/setup` (first-admin bootstrap).

**Authorization layering** (`apiserver/middleware/auth.go`): two distinct middleware layers, not one combined check —
- `RequireAuth` — missing/invalid/expired/revoked credential or banned user all collapse to a generic 401 (anti-enumeration: an attacker can't distinguish "wrong token" from "banned account" from "token for a scope that doesn't apply here").
- `RequireScope` — valid credential, but wrong scope or insufficient authority (e.g. not an admin) → 403.

**Registration invites**: admin-issued, single-use, TTL-bounded tokens (`registration_invites` table) required to self-register a new account via `POST /api/v1/auth/register`. The old TS-era "pairing invite" endpoints (`POST /api/v1/auth/pairing-invites`, `.../redeem`) are kept as tombstones on `clientapi` that return `410 Gone` — not removed outright, so any stale TS-era client gets an explicit "this is retired" response instead of a silent 404.

**TLS identity** (`internal/controlplane/tlsidentity`): the client surface generates a self-signed ECDSA P-256 certificate/key pair on first run and reuses it on every run after (`tls.LoadX509KeyPair`, regenerating only if either file is missing) — private key `0600`, matching every other on-disk secret's file-mode convention in this codebase (see `auth.BootstrapSecret`). 10-year validity, deliberately long: there's no rotation story yet, and a same-key assumption matters once pairing (below) pins this key's SPKI hash. This is HTTPS trust bootstrap only — it does not include PIN-pairing or any client-side verification, see below.

## Current network reality

- **Client API** (`127.0.0.1:3000` by default, `MAKO_CLIENT_ADDR=0.0.0.0:3000` for LAN) serves **HTTPS** with the self-signed certificate above. No client-side trust verification exists yet (no PIN-pairing, no TOFU/SPKI pinning); the Android client in `apps/client` currently accepts any certificate (`badCertificateCallback`), which is why this surface is only safe on a trusted LAN or behind a manually-configured reverse proxy. See below.
- **Admin API** (`127.0.0.1:3001`) is unchanged: **plain HTTP, loopback-bound**. Its loopback binding is its own independent security barrier (a stolen bearer token or missed authorization check still can't be exploited without actual host access) — TLS doesn't change that, and the roadmap's "HTTPS for the client surface" was always scoped to the client surface only.

## Designed, not yet built: PIN-pairing trust bootstrap

The long-term target (from the Go-backend-consolidation design work) replaces blind TOFU-over-HTTPS with Sunshine/Apollo-style PIN-confirmed pairing for the client API's self-signed certificate:

1. Client generates a PIN; user enters it on the host.
2. Both sides derive a shared secret from `salt + PIN` and exchange encrypted challenges proving possession of that secret, *before* the server's self-signed cert is trusted.
3. Client persists the server's SPKI fingerprint (not the whole certificate — a same-key cert renewal doesn't break trust).
4. Any later connection presenting a different key is rejected.

Authorization after pairing stays bearer-token based (unchanged from what's implemented today) — PIN-pairing only replaces how the TLS channel is first trusted, not how requests are authorized afterward. **Deliberately deferred** (decided with the user, 2026-10-16): although the Android client now exists, this sketch still isn't a concrete protocol (no KDF, challenge format, or rotation story specified), and building it would be unverifiable except against a simulated-client test harness. `internal/controlplane/pairing/` stays an empty stub package until that protocol is specified. Until then, the client API must stay on a trusted LAN or behind a manually-configured reverse proxy.

## Networking target (designed, not yet built)

Three distinct traffic paths are planned:

1. **Control-plane client API** — HTTPS as of this phase (self-signed, no client trust verification yet); target is PIN-paired + LAN-reachable, see above.
2. **Worker media/data ports** — the worker's pion WebRTC stack is IPv4/UDP only and advertises every routable IPv4 interface (LAN, Tailscale, other VPNs; link-local/loopback-only excluded), letting ICE choose. This is deliberate: hard-coding one LAN address would make Tailscale/VPN clients unreachable, and ICE exists to select among interfaces. `MAKO_WEBRTC_HOST_IP` is available as a debug override, not the normal path. There is still no ephemeral UDP port range. Only the control plane's client API is authenticated by bearer tokens; the media channel is protected by SDP/ICE secrecy, not application auth.
3. **Worker internal control API** — strictly `127.0.0.1`-bound, never LAN-reachable; unaffected by this phase.
