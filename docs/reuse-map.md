# Reuse map

> **Status: v0.0.1 snapshot, maintenance-only.** This is a map, not a support
> contract. The code was written as one product, not as a set of libraries;
> copying a package usually means copying its interfaces, tests, and assumptions
> too. The MIT license applies, but no compatibility promise does.

RemotePhone is a self-hosted Android-emulator runtime. A Go control plane spawns
one worker process per running instance; each worker drives an Android Studio
emulator over gRPC, encodes H.264 video and PCMU audio into WebRTC, and accepts
native multi-touch/key input from a Flutter Android client.

If you only want one piece, start with the table below. The **reuse level** is
my honest estimate after building it, not a claim that the package is cleanly
extractable.

## If you only take one thing

The three pieces with the least product-specific coupling are:

1. `internal/worker/transport/emugrpc` - a gRPC client for emulator screenshots,
   audio, touch, and key events.
2. `internal/worker/transport/webrtc` - H.264/PCMU WebRTC transport, including
   the multi-slice RTP fix that is easy to get wrong.
3. `internal/worker/runtime/androidstudio` - discover, launch, inspect, and stop
   Android Studio emulators.

Everything else is useful, but more coupled to this repository's control plane,
auth model, or AVD layout.

## What is here

| Piece | Start here | What you get | Coupling and caveats |
|---|---|---|---|
| Emulator gRPC transport | [`internal/worker/transport/emugrpc/`](../apps/server/internal/worker/transport/emugrpc/) (`client.go`, `input.go`, `audio.go`, `proto/`) | Screenshot/audio streams, multi-touch `sendTouch`, key events, emulator endpoint discovery | Generated proto is committed; no public API stability. Requires an emulator gRPC endpoint and auth token. |
| WebRTC media pipeline | [`internal/worker/transport/webrtc/`](../apps/server/internal/worker/transport/webrtc/) (`adapter.go`, `encoder.go`, `input.go`, `audio.go`, `pacer.go`) | H.264 RTP packetization, PCMU audio track, ffmpeg encoder selection, display-rotation normalization, touch-coordinate mapping | Coupled to the worker's `transport` interfaces and the emulator adapter. Read [engineering notes](engineering-notes.md) before copying `pipeH264`. |
| Emulator lifecycle | [`internal/worker/runtime/androidstudio/`](../apps/server/internal/worker/runtime/androidstudio/) (`discover.go`, `launch.go`, `inspect.go`, `stop.go`) | AVD discovery, headless launch with console/adb/gRPC ports, inspection, graceful stop, Windows/Unix process handling | Assumes a standard Android SDK/AVD layout. One emulator per worker in this design. |
| QCOW2 base-image layering | [`internal/controlplane/image/`](../apps/server/internal/controlplane/image/) plus worker-side capture code | Thin overlay per instance, capture/flatten, encryption-key handoff, base-image promotion | The strongest coupling in the repo. Capture boot verification was never finished; read the 2026-10-20 and 2026-10-27 decisions-log entries before trusting it. |
| Per-instance worker orchestration | [`internal/controlplane/placement/placement.go`](../apps/server/internal/controlplane/placement/placement.go), [`internal/controlplane/workerproc/workerproc.go`](../apps/server/internal/controlplane/workerproc/workerproc.go) | Port allocation, `exec.Command` spawn, health wait, kill, adopt-after-crash pattern | A pattern, not a library. The environment-passing bug in `workerproc.Spawn` is documented in the engineering notes. |
| Auth and pairing | [`internal/controlplane/auth/`](../apps/server/internal/controlplane/auth/), [`internal/controlplane/tlsidentity/`](../apps/server/internal/controlplane/tlsidentity/), [`internal/controlplane/apiserver/`](../apps/server/internal/controlplane/apiserver/) | Access codes, bearer tokens, self-signed TLS identity, certificate pinning | Reference only. Trusted LAN; not hardened for public internet or multi-tenant isolation. See [SECURITY.md](../SECURITY.md). |
| Flutter client | [`apps/client/lib/services/webrtc_session.dart`](../apps/client/lib/services/webrtc_session.dart), [`apps/client/lib/screens/stream_screen.dart`](../apps/client/lib/screens/stream_screen.dart), [`apps/client/lib/services/api_client.dart`](../apps/client/lib/services/api_client.dart) | WebRTC receiver, native touch sender, mDNS discovery, certificate pinning, instance UI | Android only. The shared-renderer lifecycle and touch mapping are the two things worth reading first. |
| Owner console | [`apps/server/adminui/`](../apps/server/adminui/) | React console embedded into the control plane with `go:embed` | Owner-only, loopback-only, no login. Not a multi-tenant admin UI. |
| Shared wire types | [`internal/contracts/types.go`](../apps/server/internal/contracts/types.go) | DTO shapes used at process boundaries | Small and currently not a stable public API. |

## What is not reusable

- **A production deployment.** There is no installer, no CI release pipeline, no
  upgrade/migration story, and no GPU capacity enforcement.
- **A way around AAA mobile anti-cheat.** The target games abort in the emulator's
  x86-to-ARM translation layer. That is an environment boundary, not a worker bug.
  Do not start here.
- **A browser streaming client.** There is none; the only streaming client is the
  Flutter Android app. Old React `<video>` code is dead weight, not a spec.
- **A security boundary for untrusted users.** The model is a trusted LAN plus
  self-signed TLS and certificate pinning. Do not expose it to the public internet.
- **Game assets, APKs, base images, or certificates.** They are not in the repo,
  and should not be added.

## Go import status

This repository is **not importable as a Go dependency** in its current form:

- `apps/server/go.mod` declares `module remotephone`, which is not a fetchable
  module path.
- The reusable packages live under `internal/`. Go's internal-package rule blocks
  imports from other modules, even with a `replace` directive.
- `webrtc` additionally depends on `worker/host` and `worker/transport`, expects
  an ffmpeg binary, and is constructed around one worker instance.

If you need a Go dependency, copy the package under the MIT license or extract a
separate module. `emugrpc` is the cleanest candidate: it only depends on gRPC and
its generated proto. `webrtc` needs a public-API pass first. Restructuring the
frozen snapshot just to make `go get` work costs more than copying one package.

## Recommended reading order

1. [Architecture overview](architecture/overview.md) - process topology and the
   package map.
2. [Decisions log](architecture/decisions-log.md) - especially the 2026-10-28
   anti-cheat entry, the 2026-10-20 capture entry, and the 2026-10-14 worker
   lifecycle entry.
3. [Engineering notes](engineering-notes.md) - the non-obvious failures.
4. The `doc.go` and tests next to whichever package you want to borrow.

## If you copy code

- Keep the MIT license notice and attribute the project.
- Replace the auth/TLS layer before exposing anything beyond a trusted LAN.
- Expect to replace the persistence schema and migrations; they describe this
  product's domain, not a general Android runtime API.
- Run one real emulator integration test. Most packages have fakes that prove the
  call sequence, not that a spawned process can actually find an emulator.
- Do not ship game APKs or captured base images with your fork.
