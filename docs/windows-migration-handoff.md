# Handoff: LAN/real-device testing + AMD GPU verification on Windows

## Who this is for

A fresh agent (no prior context) picking up work on the user's Windows PC (AMD GPU). This doc exists because two pieces of work got blocked on the current dev machine (a corporate-managed Mac) for reasons specific to that machine, not to mako's code — both are expected to just work on an unmanaged Windows PC, but need real verification there.

## Problem 1: this Mac blocks inbound LAN connections to mako's own processes (not expected to reproduce on Windows, but verify)

### Symptom

A real phone (connected via `adb` over WiFi, confirmed working — see below) could not log in to the mako client API running on this Mac's LAN IP. The HTTPS login request never arrived at all: `netstat` on the server side during the login attempt showed zero connection attempts on port 3000, not even a half-open one.

### What's ruled out (do not re-investigate these)

- **Not AP/WiFi client isolation.** The phone's `adb connect <mac-ip>:5555`-style WiFi debugging already works perfectly between these two devices on this same WiFi network — direct device-to-device LAN traffic is not blocked by the network itself.
- **Not the visible Application Firewall.** `socketfilterfw --getappblocked /path/to/mako-worker` reports "permitted" — yet connections to it still silently died. Whatever is blocking this operates at a layer the user-visible firewall UI (and its `socketfilterfw` CLI) doesn't control or show.
- **Not a TLS/certificate problem.** Confirmed by testing with plain HTTP too (no TLS at all) — same silent connect-then-die behavior. `curl`/`openssl s_client` from the Mac to its own LAN IP showed the TCP handshake complete (`Connected to 192.168.1.6 port 3000`) but the TLS ClientHello got an immediate `unexpected eof` — so something intercepts/kills the stream after the TCP handshake, not during it.
- **Not specific to the emulator's virtual NAT.** Originally suspected this was an Android-emulator-specific NAT hairpin limitation (emulator trying to reach its own host machine). Ruled out by reproducing the identical failure from a **real phone** on the actual LAN, and separately by reproducing it with a plain Go program making no emulator involvement at all.
- **Not about code signing alone.** Wrapped `mako-controlplane` in a minimal ad-hoc-signed `.app` bundle (with `Info.plist` + `NSLocalNetworkUsageDescription`) on the theory that macOS's "Local Network" privacy permission (which gates why `LocalSend`, a properly Developer-ID-signed/notarized app, works for LAN file transfer on this same Mac) was the blocker. No change in behavior — still silently died identically.
- **Not something the user/IT can toggle via normal Settings/CLI on this machine**: `socketfilterfw --add`/`--unblockapp` explicitly refused with *"Firewall settings cannot be modified from command line on managed Mac computers."* This machine is MDM-managed.

### Current best hypothesis (unconfirmed — do not assume this is definitely right)

A deeper, MDM-enforced network security policy — likely the already-present Netskope endpoint agent (confirmed installed and already known to intercept HTTPS for other reasons this session, e.g. Gradle/Maven dependency fetches needed a custom root CA imported into the JDK truststore) or a compliance-profile-pushed packet filter — defaults to killing unsolicited inbound connections to unsigned/unapproved local server processes, regardless of protocol (TCP or UDP) and regardless of whether the visible Application Firewall says "permitted."

This also fully explains an earlier, separately-diagnosed symptom: WebRTC ICE/STUN connectivity checks from an Android emulator client to the worker's negotiated candidates (host LAN candidate, VPN-tunnel-interface candidate, and public server-reflexive candidate — all three) timed out identically with zero responses, even though the destination ports were confirmed genuinely listening (a raw UDP packet via `nc` bound to a wildcard address *did* get through; the same packet to a Go program's own listening socket on the exact same address did not).

### What to do on the Windows PC

1. **This almost certainly just works there** — assume a normal, non-managed Windows machine's firewall will show the standard "Windows Defender Firewall has blocked some features of this app" prompt the first time `mako-worker`/`mako-controlplane` tries to listen, and that approving it (normal, expected, user-controllable) resolves everything. This is NOT the same situation as the Mac (which refuses to let the user configure it at all).
2. Build and run both binaries (`cmd/mako-controlplane`, `cmd/mako-worker`) on Windows. The client-facing HTTP server's bind address is already configurable: set `MAKO_CLIENT_ADDR=0.0.0.0:3000` (added this session, see `cmd/mako-controlplane/main.go`) so it's reachable from other LAN devices, not just loopback.
3. Get a real Android phone on the same WiFi as the Windows PC, point the Flutter client's Server URL field at the PC's real LAN IP (`https://<pc-lan-ip>:3000` — NOT `10.0.2.2`, that's an Android-emulator-only loopback alias, meaningless on real hardware), and confirm:
   - Login/signaling HTTPS traffic reaches the server.
   - A full WebRTC session actually connects (ICE succeeds) and streams video with responsive touch input.
4. If this works cleanly (expected), there's nothing further to build for this problem — it was a Mac-specific environment constraint, not a mako bug. If it does NOT work the same way on Windows, that would be a genuinely new and important finding — re-open investigation rather than assuming another environment-specific dead end.
5. Related, explicitly deferred by the user until after real-device LAN testing works: **mDNS-based service discovery**, so a phone can find the server without the user manually typing an IP. Pick this up once step 3 is solid.

## Problem 2: verify the AMD GPU hardware encoder path (never tested on real AMD hardware)

### Context

`internal/worker/transport/webrtc/encoder.go` added an `h264_amf` candidate for `GOOS == "windows"` (AMD's Advanced Media Framework, ffmpeg's Windows-only hardware H264 encoder for AMD GPUs) this session. It was added based on documentation and the existing nvenc/vaapi code's pattern — **this dev environment (macOS) has no AMD GPU, so none of it has ever actually run.** The code comment is explicit about this: *"nvenc/vaapi/amf flag specifics are necessarily best-effort here — this development environment has no Linux or AMD GPU to verify them against."*

Candidate order for Windows (`defaultEncoderCandidates`): `h264_nvenc` → `h264_amf` → `libx264`. On a real AMD-GPU Windows box, `h264_nvenc` should fail to probe (no NVIDIA driver) and it should fall through to `h264_amf`.

### What to verify

1. `ffmpeg -encoders | findstr amf` (or `grep amf` in a POSIX-ish shell) — confirm the Windows ffmpeg build actually has `h264_amf` compiled in. If not, the build/install needs a different ffmpeg distribution.
2. Confirm `SelectEncoder`'s real probe (`RealEncoderProber.Probe`, a throwaway `ffmpeg -f lavfi -i color=black:s=64x64:d=0.2 -c:v h264_amf -f null -`) actually succeeds on this hardware — i.e., the AMF driver/runtime is actually initialized, not just compiled into ffmpeg.
3. Confirm the actual capture pipeline (`buildFFmpegArgs`'s `h264_amf` branch: `-usage lowlatency -quality speed -rc cbr -profile:v constrained_baseline -level:v 3.1`) produces output pion/WebRTC can actually decode end-to-end — connect a real client and confirm video renders (not just that ffmpeg exits 0). Pay close attention to the same class of bug that bit the videotoolbox path earlier this session: a profile/level mismatch between what the hardware encoder actually emits and what's been negotiated can look "connected" at the transport layer while silently producing an undecodable stream (`framesReceived` grows, `framesDecoded` stays at 0). Use the client's WebRTC stats polling (`webrtc_session.dart`'s periodic `getStats()` logging) to confirm `framesDecoded` tracks `framesReceived`, not just that the peer connection reports "Connected".
4. If `constrained_baseline` isn't actually a valid value for AMF's `-profile:v` on this ffmpeg build (profile enum values for `h264_amf` have varied across ffmpeg versions — some only accept `main`/`high`), adjust `buildFFmpegArgs`'s `h264_amf` case accordingly and update the comment that currently flags this as unverified.
5. Update `docs/plans/windows-migration-handoff.md` (this file) or `encoder.go`'s comments once verified, removing the "unverified/best-effort" caveats so future readers know this path is now confirmed, not just hopeful.

### Setting up the dev environment on Windows

- Android Emulator: install via Android Studio / `sdkmanager` as usual. On AMD CPUs, make sure **Windows Hypervisor Platform (WHPX)** is enabled (Intel HAXM does not support AMD CPUs) — this is a standard, well-documented Android emulator requirement on Windows+AMD, not mako-specific.
- Go toolchain, ffmpeg (with AMF support), and the Android SDK/platform-tools need to be on `PATH` or pointed at via the existing env vars (`MAKO_ANDROID_SDK_ROOT`, `MAKO_FFMPEG_PATH`, `MAKO_ANDROID_EMULATOR_PATH` — see `internal/worker/config/config.go`).
- The per-instance AVD-cloning mechanism added this session (`internal/worker/runtime/androidstudio/adapter.go`'s `ensureInstanceAVD`) does plain file copies and writes a `.ini` with a `path=` key — this should be Windows-path-safe (uses `filepath.Join` throughout), but has only been tested on macOS. If instance creation fails on Windows, check this first — Windows AVD `.ini` files conventionally use the same `path=<dir>` format, but worth confirming the emulator resolves backslash vs. forward-slash paths correctly in that value.

## Reference: what shipped this session (for context, not re-work)

- `internal/worker/transport/webrtc/encoder.go` — `h264_amf` candidate (Problem 2 above).
- `internal/worker/runtime/androidstudio/adapter.go` — per-instance AVD cloning for user data isolation (unrelated to either problem above, already verified working on macOS; mentioned here only because it's new code a Windows agent will also be running).
- `cmd/mako-controlplane/main.go` — `MAKO_CLIENT_ADDR` env var for configuring the client API's bind address (added specifically to attempt the LAN phone test described in Problem 1).
- `apps/client/android/app/src/main/AndroidManifest.xml` — added `INTERNET`/`ACCESS_NETWORK_STATE`/etc. permissions, fixing an unrelated native SIGABRT crash (missing permissions caused a `SecurityException` inside WebRTC's JNI network-monitor code that the library didn't handle gracefully). This fix is unrelated to Problems 1/2 and already confirmed working — no further action needed.
