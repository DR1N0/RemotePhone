# Security

## Supported versions

None. `v0.0.1` is a frozen snapshot. No security updates are promised, and no
version is supported for production use.

## Deployment warning

RemotePhone was built for a **trusted LAN**. Do not expose it to the public
internet.

- The client API uses a self-signed certificate and certificate pinning, not a
  hardened public-key infrastructure.
- Pairing uses one-time access codes. Not every redemption path has brute-force
  rate limiting.
- The owner console is loopback-only and tokenless by design; that is an owner
  boundary, not a multi-user authorization system.
- Workers spawn local emulator and helper processes with the server's privileges.
- APKs, emulator images, and captured base images are untrusted code. A malicious
  guest may be able to reach the host network.

## Known limitations

- No multi-tenant isolation or resource quotas.
- No GPU capacity enforcement; instances share the host GPU and encoder.
- No installer, upgrade/migration story, or CI release pipeline.
- No audit-grade authorization review.
- No DDoS protection, WAF, or public-internet hardening.

## Reporting

If GitHub private vulnerability reporting is enabled for this repository, use it.
Otherwise open an issue **without exploit details** and ask for a private channel.
There is no response SLA.
