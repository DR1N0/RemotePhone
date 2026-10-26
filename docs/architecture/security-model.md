# Security model

## Implemented today

**Password hashing** (`apps/server/internal/controlplane/auth/password.go`): `scrypt`, N=16384, r=8, p=1, 64-byte derived key, 16-byte random salt, encoded as `scrypt-v1$16384$8$1$64$<salt-b64url>$<key-b64url>`. Verification parses the stored parameters and rejects any parameter *greater* than today's constants (a ceiling check — guards against a future lowering of the constants silently trusting a higher embedded value from an old hash). Comparison is `crypto/subtle.ConstantTimeCompare`, timing-safe.

**Bearer tokens** (`auth/token.go`): 32 cryptographically random bytes, base64url-encoded without padding (43 chars, 256 bits of entropy). Looked up by plain (unsalted) SHA-256 hex digest — unsalted deliberately, since the input is already high-entropy random data, not a low-entropy secret a dictionary attack could target.

**Bootstrap secret** (`auth/bootstrapsecret.go` + platform files): exactly 32 random bytes, base64url-encoded (43 chars). On Unix the file is created with mode exactly `0600` in a `0700` directory and read through `O_NOFOLLOW` with an exact-mode check; on Windows the file inherits the per-user NTFS ACL of its directory and a reparse point/symlink at the path is rejected before reading (POSIX mode bits are meaningless there). Writes go through a temp file + `os.Link`, so the published name never exposes a partially written secret. Used once, to authorize `POST /api/v1/auth/setup` (first-admin bootstrap).

**Authorization layering** (`apiserver/middleware/auth.go`): two distinct middleware layers, not one combined check —
- `RequireAuth` — missing/invalid/expired/revoked credential or banned user all collapse to a generic 401 (anti-enumeration: an attacker can't distinguish "wrong token" from "banned account" from "token for a scope that doesn't apply here").
- `RequireScope` — valid credential, but wrong scope or insufficient authority (e.g. not an admin) → 403.

**Registration invites**: legacy admin-issued tokens (`registration_invites`) still exist server-side, but the Android client now uses unified access codes for account creation and device pairing. The old TS-era "pairing invite" endpoints are kept as `410 Gone` tombstones.

**TLS identity** (`internal/controlplane/tlsidentity`): the client surface generates a self-signed ECDSA P-256 certificate/key pair on first run and reuses it on every run after (`tls.LoadX509KeyPair`, regenerating only if either file is missing) — private key `0600`, matching every other on-disk secret's file-mode convention in this codebase (see `auth.BootstrapSecret`). 10-year validity; clients pin the certificate's SHA-256 after a successful access-code redemption or login, so an unexpected regeneration requires a new code. This is HTTPS trust bootstrap plus access-code pairing.

## Current network reality

- **Client API** (`127.0.0.1:3000` by default, `MAKO_CLIENT_ADDR=0.0.0.0:3000` for LAN) serves **HTTPS** with the self-signed certificate above. A new device redeems a single-use **access code** to establish trust, then logs in. The code is either a self code (ordinary user, bound to that account) or an admin-issued unbound code that can create a new ordinary user or pair an existing account. The response includes the certificate SHA-256; the client compares it with the certificate actually presented on that TLS connection and stores it in secure storage. Every later connection pins that fingerprint; a certificate change rejects the connection. The first redemption is still TOFU over TLS, not a PAKE, so an active MITM on the very first contact could substitute a certificate — use a trusted LAN for first contact.
- **Owner console** (`127.0.0.1:3001`) is **plain HTTP, loopback-bound**. Reaching it requires host/SSH access, which is treated as owner proof, so the owner routes do not require a bearer token. They reject browser `Origin` headers, non-JSON POSTs, and non-loopback Host values to prevent CSRF/DNS rebinding. Existing admin routes also retain bearer-token authorization; the owner routes provide access-code generation and password reset as the out-of-band recovery path.

## Implemented: access-code certificate pinning

The client API's self-signed certificate is trusted through a single-use access code:

1. Control plane seeds its startup PIN (or `MAKO_PAIRING_PIN`) as an unbound bootstrap access code and logs it with the certificate SHA-256.
2. An authenticated admin on port 3000 can create an unbound code (`POST /api/v1/access-codes`), or an ordinary user can create a self code. The owner console on 3001 can create either without a token.
3. The client connects with a temporary unpinned HTTP client and submits `code`, `username`, and `password` to `POST /api/v1/access-codes/redeem`.
4. The server consumes the code and either: creates a new ordinary account if the username does not exist (unbound code), or verifies the existing account password and adds this device. It returns the certificate SHA-256; the client checks that it matches the certificate actually presented on this TLS connection, then stores it via `flutter_secure_storage`.
5. All later clients pin that certificate hash; a different certificate makes the TLS handshake fail. Pins are stored by URL and by stable server ID. The URL pin is the fast path for the same address. For a new LAN address, the client fetches `/server/info` with a temporary unpinned client, reads the server ID, and trusts the new URL only if the certificate observed on that same connection matches the server-ID pin. A spoofed server ID therefore cannot bypass the pin. If the certificate or server ID changed, the code field is required again. If an exact-URL pin exists but the certificate changed, the login handshake fails and the client switches to the access-code path.

This is code-gated TOFU, not a PAKE: an active MITM during the very first contact could still substitute its certificate. It removes blind trust-on-first-use and passive interception; a future revision can add a PAKE/SAS for first-contact resistance. Authorization after pairing remains bearer-token based — pairing only establishes TLS trust, not user identity.

The startup bootstrap code is regenerated on each restart unless `MAKO_PAIRING_PIN` is set; already-paired clients keep working (their pinned fingerprint is unchanged). Rotating the server certificate requires a new access code.

## Networking target (designed, not yet built)

Three distinct traffic paths are planned:

1. **Control-plane client API** — HTTPS with the self-signed certificate, access-code-paired and certificate-pinned; LAN-reachable via `MAKO_CLIENT_ADDR`.
2. **Worker media/data ports** — the worker's pion WebRTC stack is IPv4/UDP only and advertises every routable IPv4 interface (LAN, Tailscale, other VPNs; link-local/multicast/IPv6 excluded, loopback kept for local probes), letting ICE choose. This is deliberate: hard-coding one LAN address would make Tailscale/VPN clients unreachable, and ICE exists to select among interfaces. `MAKO_WEBRTC_HOST_IP` is available as a debug override, not the normal path. There is still no ephemeral UDP port range. Only the control plane's client API is authenticated by bearer tokens; the media channel is protected by SDP/ICE secrecy, not application auth.
3. **Worker internal control API** — strictly `127.0.0.1`-bound, never LAN-reachable; unaffected by this phase.
