# Security model

## Implemented today

**Password hashing** (`apps/server/internal/controlplane/auth/password.go`): `scrypt`, N=16384, r=8, p=1, 64-byte derived key, 16-byte random salt, encoded as `scrypt-v1$16384$8$1$64$<salt-b64url>$<key-b64url>`. Verification parses the stored parameters and rejects any parameter *greater* than today's constants (a ceiling check — guards against a future lowering of the constants silently trusting a higher embedded value from an old hash). Comparison is `crypto/subtle.ConstantTimeCompare`, timing-safe.

**Bearer tokens** (`auth/token.go`): 32 cryptographically random bytes, base64url-encoded without padding (43 chars, 256 bits of entropy). Looked up by plain (unsalted) SHA-256 hex digest — unsalted deliberately, since the input is already high-entropy random data, not a low-entropy secret a dictionary attack could target.

**Bootstrap secret** (`auth/bootstrapsecret.go`): exactly 32 random bytes, base64url-encoded (43 chars), file mode exactly `0600`, parent directory `0700`, written via `O_CREATE|O_EXCL` (never overwrites an existing file), read via `os.Lstat` first and rejected if it's a symlink. Used once, to authorize `POST /api/v1/auth/setup` (first-admin bootstrap).

**Authorization layering** (`apiserver/middleware/auth.go`): two distinct middleware layers, not one combined check —
- `RequireAuth` — missing/invalid/expired/revoked credential or banned user all collapse to a generic 401 (anti-enumeration: an attacker can't distinguish "wrong token" from "banned account" from "token for a scope that doesn't apply here").
- `RequireScope` — valid credential, but wrong scope or insufficient authority (e.g. not an admin) → 403.

**Registration invites**: admin-issued, single-use, TTL-bounded tokens (`registration_invites` table) required to self-register a new account via `POST /api/v1/auth/register`. The old TS-era "pairing invite" endpoints (`POST /api/v1/auth/pairing-invites`, `.../redeem`) are kept as tombstones on `clientapi` that return `410 Gone` — not removed outright, so any stale TS-era client gets an explicit "this is retired" response instead of a silent 404.

## Current network reality

Both HTTP surfaces run **plain HTTP, loopback-bound**:
- Client API: `127.0.0.1:3000`
- Admin API: `127.0.0.1:3001`

No TLS, no PIN pairing, no LAN exposure exists in the Go port yet. This is adequate for same-host development only.

## Designed, not yet built: PIN-pairing trust bootstrap

The long-term target (from the Go-backend-consolidation design work) replaces blind TOFU-over-HTTPS with Sunshine/Apollo-style PIN-confirmed pairing for the client API's self-signed certificate:

1. Client generates a PIN; user enters it on the host.
2. Both sides derive a shared secret from `salt + PIN` and exchange encrypted challenges proving possession of that secret, *before* the server's self-signed cert is trusted.
3. Client persists the server's SPKI fingerprint (not the whole certificate — a same-key cert renewal doesn't break trust).
4. Any later connection presenting a different key is rejected.

Authorization after pairing stays bearer-token based (unchanged from what's implemented today) — PIN-pairing only replaces how the TLS channel is first trusted, not how requests are authorized afterward. The `registration_invites`/`pairing/` package naming already anticipates this but `internal/controlplane/pairing/` and `internal/controlplane/tlsidentity/` are currently empty stub packages. Until this is built, the client API must stay loopback-only or behind a manually-configured reverse proxy — there is no safe way to expose `127.0.0.1:3000` directly to a LAN today.

## Networking target (designed, not yet built)

Three distinct traffic paths are planned, only the first of which exists today:

1. **Control-plane client API** — today plain HTTP/loopback; target is HTTPS + PIN-paired, LAN-reachable.
2. **Worker media/data ports** — not yet relevant; no worker-lifecycle code runs workers under control-plane supervision yet.
3. **Worker internal control API** — not yet relevant for the same reason; target is strictly `127.0.0.1`-bound, never LAN-reachable.
