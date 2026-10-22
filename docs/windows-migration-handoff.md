# Handoff: LAN/real-device testing + AMD GPU verification on Windows

## Status 2026-10-22 — Windows host is working

Both problems below have been worked on the Windows machine. Summary:

- **Server builds/tests/serves natively on Windows 11** (Go 1.27.3, Ryzen 9700X, RX 9070). The POSIX-only blockers were fixed (bootstrap-secret platform split, Win32 process liveness/stop, adb-first graceful emulator shutdown, `.exe`/SDK path resolution, `STUN_URL=none`); the full Go test suite passes natively.
- **AMD `h264_amf` is confirmed working at real capture size.** `ffmpeg -encoders` has it, a 60-frame 540x1200 rawvideo pipe encoded and decoded cleanly (Constrained Baseline, level 3.1), and the worker now logs `selected encoder h264_amf`. The old 64x64 probe was the actual bug: AMF's `encoder->Init()` fails at that size, so `SelectEncoder` silently fell through to `libx264`. The probe now receives the real capture dimensions. `-bf 0` was removed from the AMF branch (it produced a noisy AMF warning).
- **End-to-end verified on Windows**, including the Android client in an emulator: controlplane + worker + two concurrent emulator instances on distinct ports, both streaming 30fps H.264; the Flutter client listed both instances and streamed one with `framesDecoded` tracking `framesReceived` (~5ms decode).
- **Audio + multi-touch added.** Emulator `streamAudio` → 8 kHz mono PCMU WebRTC track; a 440 Hz guest tone was captured at exactly 440.0 Hz on the host probe. Emulator `sendTouch` now carries per-finger pointer IDs; `getevent` shows two simultaneous MT slots. ICE advertises every routable IPv4 interface (LAN + Tailscale) and lets ICE choose; no Tailscale-specific config.
- **Real-phone LAN video verified**: Xiaomi phone at `192.168.1.4` connected to the PC at `192.168.1.7`, ICE selected that pair, and video ran ~30fps with `framesDecoded` tracking `framesReceived` over the test window. The emulator-as-client hairpin remains lossy and is not representative; use a real phone.
- **Real-phone audio verified after a guest cold reboot**: the emulator's quickboot snapshot had restored a wedged audio state (`streamAudio` sent 136-byte near-silence packets at ~93/s); after `adb reboot` + fresh Tone, packets became 1920 bytes/RMS≈192 at ~95/s and the phone went to ~50 audio packets/s (`audioLevel=0.0084`). If audio is silent after an instance restart, cold-boot the guest rather than trusting the snapshot. `MAKO_AUDIO_DEBUG=1` logs the packet RMS/peak diagnostics. The Android client now configures `AndroidAudioConfiguration.media` at startup, so the audio uses the speaker and media volume instead of call volume/earpiece.
- **Full-screen client + guest-native navigation**: the phone's system bars are hidden via a native `mako/window` channel; the worker switches the guest to three-button navigation on each offer, so Back/Home/Recents are tapped inside the video. The phone's Back gesture is the only client exit and disconnects the stream. Verified on the real phone.
- **Headless is now the worker default** (`MAKO_HEADLESS=1`; one worker per instance). Verified on the real phone: `-no-window`/`qemu-system-x86_64-headless.exe`, ~30fps video, ~50 audio packets/s, touch and three-button nav working. `MAKO_HEADLESS=0` is debug-only. The emulator also runs with `-audio none` by default (`MAKO_EMULATOR_AUDIO=auto` restores host sound), so the host speakers are silent while `streamAudio` still works (host probe: 440Hz). Windows service/Session 0 GPU/audio behavior is still untested; prefer a logon-triggered scheduled task first.
- **Problem 1 (LAN from a real phone) is still open.** The verification above used an Android emulator as the client, not a physical phone. The Mac-specific firewall/Netskope theory behind Problem 1 is still unconfirmed on Windows; try a real phone with `MAKO_CLIENT_ADDR=0.0.0.0:3000` and a firewall allow rule.

Path references below predate the move of the Go server into `apps/server/`; `internal/worker/...` means `apps/server/internal/worker/...`, `cmd/...` means `apps/server/cmd/...`.

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
5. Update `docs/windows-migration-handoff.md` (this file) or `encoder.go`'s comments once verified, removing the "unverified/best-effort" caveats so future readers know this path is now confirmed, not just hopeful.

### Setting up the dev environment on Windows

- Android Emulator: install via Android Studio / `sdkmanager` as usual. On AMD CPUs, make sure **Windows Hypervisor Platform (WHPX)** is enabled (Intel HAXM does not support AMD CPUs) — this is a standard, well-documented Android emulator requirement on Windows+AMD, not mako-specific.
- Go toolchain, ffmpeg (with AMF support), and the Android SDK/platform-tools need to be on `PATH` or pointed at via the existing env vars (`MAKO_ANDROID_SDK_ROOT`, `MAKO_FFMPEG_PATH`, `MAKO_ANDROID_EMULATOR_PATH` — see `internal/worker/config/config.go`).
- The per-instance AVD-cloning mechanism added this session (`internal/worker/runtime/androidstudio/adapter.go`'s `ensureInstanceAVD`) does plain file copies and writes a `.ini` with a `path=` key — this should be Windows-path-safe (uses `filepath.Join` throughout), but has only been tested on macOS. If instance creation fails on Windows, check this first — Windows AVD `.ini` files conventionally use the same `path=<dir>` format, but worth confirming the emulator resolves backslash vs. forward-slash paths correctly in that value.

## Reference: what shipped this session (for context, not re-work)

- `internal/worker/transport/webrtc/encoder.go` — `h264_amf` candidate (Problem 2 above).
- `internal/worker/runtime/androidstudio/adapter.go` — per-instance AVD cloning for user data isolation (unrelated to either problem above, already verified working on macOS; mentioned here only because it's new code a Windows agent will also be running).
- `cmd/mako-controlplane/main.go` — `MAKO_CLIENT_ADDR` env var for configuring the client API's bind address (added specifically to attempt the LAN phone test described in Problem 1).
- `apps/client/android/app/src/main/AndroidManifest.xml` — added `INTERNET`/`ACCESS_NETWORK_STATE`/etc. permissions, fixing an unrelated native SIGABRT crash (missing permissions caused a `SecurityException` inside WebRTC's JNI network-monitor code that the library didn't handle gracefully). This fix is unrelated to Problems 1/2 and already confirmed working — no further action needed.
