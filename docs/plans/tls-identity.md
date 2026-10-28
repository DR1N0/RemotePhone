# Phase 5 (scoped): TLS identity for the client surface

**Status: Done.**

Living plan — edit in place as scope evolves; not dated/immutable. See `roadmap.md` for how this fits the overall sequence.

**Scope decision (discussed with the user before starting):** roadmap Phase 5 is "PIN-pairing trust bootstrap, HTTPS for the client surface" — two different things. PIN-pairing is a mutual client/server protocol, but there is no native `remotephone-client` yet to pair with or verify a handshake against, and `security-model.md`'s own description of it is a 4-step sketch, not a concrete protocol (no KDF, challenge format, or rotation story specified). Unlike every prior phase (each verified against a real counterpart — the real `remotephone-worker` binary, or TS as a literal spec), building the PIN-pairing handshake now would be unverifiable except against a test harness simulating a client, with real risk of a client-side mismatch surfacing later. **Decided scope: HTTPS + self-signed certificate infrastructure only.** PIN-pairing stays deferred — `internal/controlplane/pairing/` is untouched, still a stub.

**Goal:** the client HTTP surface (`127.0.0.1:3000`) serves real TLS instead of plain HTTP, using a self-signed certificate the control plane generates and persists on first run. The admin surface stays plain HTTP/loopback-only — `security-model.md` already documents that distinction as intentional (loopback binding is its own security barrier, independent of transport encryption), and the roadmap text says "HTTPS for the **client** surface" specifically.

## Scope

### `internal/controlplane/tlsidentity` (new, replaces the stub)
`Identity.GetOrCreate() (tls.Certificate, error)`: loads an existing cert/key pair from disk (`tls.LoadX509KeyPair`), or generates a fresh self-signed ECDSA P-256 pair and persists it if either file is missing. Matches `auth.BootstrapSecret`'s on-disk-secret conventions where they apply: private key written `0600`, directory created `0700`. The certificate itself is `0644` (public by nature). 10-year validity — deliberately long, since there's no renewal/rotation process yet and a future pairing layer will pin this key's SPKI hash, so an unplanned regeneration would break every already-paired client once that layer exists. `DNSNames: ["localhost"]`, loopback `IPAddresses` — sufficient for the loopback-only deployment this phase targets; real LAN-reachable SANs are the pairing phase's problem, not this one's.

### `cmd/remotephone-controlplane/main.go`
New env vars `REMOTEPHONE_TLS_CERT_PATH`/`REMOTEPHONE_TLS_KEY_PATH`, defaulting alongside the DB file (`<dbPath>.cert.pem`/`<dbPath>.key.pem`, mirroring `REMOTEPHONE_BOOTSTRAP_SECRET_PATH`'s `<dbPath>.bootstrap-secret` convention). `clientSrv` gets `TLSConfig` set from `tlsidentity.Identity.GetOrCreate()` and starts via `ListenAndServeTLS("", "")` (empty paths — the cert is already loaded into `TLSConfig`, matching the standard idiom for a programmatically-supplied certificate). `adminSrv` is unchanged (plain HTTP, loopback).

## Explicitly out of scope for this phase

- PIN-pairing handshake (PIN-derived shared secret, challenge/response, SPKI fingerprint pinning/TOFU) — `internal/controlplane/pairing/` stays a stub.
- Certificate rotation/renewal policy — out of scope until pairing (which needs to reason about "same key, new cert" vs. "different key") actually exists.
- LAN-reachable SANs / exposing the client surface beyond loopback — no safe way to do that yet per `security-model.md`, unchanged by this phase.
- Any change to the admin surface's transport (stays plain HTTP/loopback).

## Verification

Unit-tested (`tlsidentity`: generation produces a parseable, correctly-dated cert; a second call reuses the same persisted serial number; file permissions). Manually verified against the real binary: the client surface answers `https://127.0.0.1:3000/healthz` (self-signed, `curl -k` needed — expected, no pairing/TOFU trust exists yet) and no longer answers plain HTTP on the same port; the admin surface is unaffected (still plain HTTP on `127.0.0.1:3001`).
