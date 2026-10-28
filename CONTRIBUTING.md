# Contributing

RemotePhone is a **finished experiment** (v0.0.1). It is public as a reference
implementation, not as an actively maintained project.

## What is welcome

- Corrections to documentation, build instructions, or comments that are factually
  wrong.
- Small, focused fixes for defects that prevent the snapshot from building or
  being understood.
- Reproducible bug reports that document the host OS, emulator version, Android
  image, and exact failure. There is no response SLA.

## What is not

- New features, productization, multi-tenant/cloud work, or large refactors. The
  code is frozen for a reason.
- Attempts to bypass game anti-cheat or to make AAA mobile titles run through the
  x86-to-ARM translation layer. That is an environment boundary, not a bug.
- Changes that add game APKs, ROMs, base images, credentials, certificates,
  databases, or `.bin/` artifacts.

## Before opening a pull request

- Run `go test ./...` in `apps/server`.
- Run `flutter analyze` in `apps/client` if you touched Dart.
- Keep the change small and explain what you verified on real hardware or a real
  emulator. Unit tests with fakes are not enough for process-spawning or
  emulator-lifecycle changes.
- Update the relevant document in `docs/` if behavior or a known limitation
  changes.

## Security

Read [`SECURITY.md`](SECURITY.md) first. Do not open a public issue containing
exploit details.
