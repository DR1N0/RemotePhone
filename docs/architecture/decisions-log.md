# Decisions log

This project previously tracked design decisions as a trail of dated, immutable spec/plan documents (`docs/superpowers/specs/`, `docs/superpowers/plans/`). That methodology is retired and those folders have been deleted (2026-10-13) — this log, plus the other files in `docs/architecture/`, is what replaces them as the current-state source of truth.

**Why this log exists:** `docs/architecture/*.md` describe current reality and get edited in place. When a change deviates from what those files previously said, or introduces something non-obvious that isn't self-explanatory from the code, add an entry here — short, self-contained, no dangling references to documents that might not exist later — and update the relevant `architecture/*.md` file in the same change.

Entries are reverse-chronological.

---

### 2026-10-27 — Embedded, login-free owner console

The former `apps/admin` React app moved to `apps/server/adminui` and is embedded into `mako-controlplane` with `go:embed`. It is served at `http://127.0.0.1:3001/admin/`; the built `dist` is committed so a clean `go build` does not require Node.

The console has no login. Reaching the loopback listener is the owner proof. `/api/v1/admin/*` on 3001 now accepts requests without a bearer token and attributes them to the reserved `_SYSTEM` principal; a bearer token is still accepted for programmatic clients. Owner requests are guarded by loopback Host, same-origin/localhost Origin, and `application/json`. Native app client routes on 3000 are a separate `ServeMux` and are unchanged.

`_SYSTEM` is a real hidden `users` row (`user_type=system`, no username/password) with a `_SYSTEM` credential so audit foreign keys remain valid. User listings and setup/admin counts exclude it. Usernames must start with a letter or digit; leading underscores are reserved for internal identities.

The UI is redesigned into Overview / Users / Instances / Audit. Access codes and account removal are actions inside Users (invite a new user, create a user-specific code, or remove an account and immediately issue a fresh invite), not separate pages. The owner never sets or reads a user's password; a removed account's username is released and the user recreates the account. Login, Invitations, Session diagnostic, and a duplicated Server page were removed; server identity remains on Overview. The visual language follows the Android client: black/white surfaces with a single cat's-eye gold accent.

---

### 2026-10-26 — Certificate pin lives only in the server profile

Removed the pre-release `mako_pin_<url>` and `mako_server_pin_<serverId>` stores. The certificate pin is now only `ServerProfile.certSha256`, keyed by the stable server UUID.

- Trust always starts by fetching `/server/info` over an unpinned temporary connection and observing the actual certificate on that same connection.
- The returned server ID selects the saved profiles; trust succeeds only if the observed certificate hash equals `profile.certSha256`.
- URL is only an address. Two servers on different LANs that happen to use the same IP are distinguished by their server IDs and separate profiles.
- `serverId` + certificate are generated once at server initialization and treated as immutable. A certificate change for the same server ID is an identity conflict, not an automatic update; it requires forgetting the profile and re-pairing.
- Forget server / Forget all remove the pin by removing the profile: re-adding requires an access code. This intentionally replaces the earlier keep-pin-on-forget behavior.
- Access-code redemption refuses to overwrite an existing profile whose certificate differs.

This is a deliberate pre-release breaking change; old pin keys are ignored and cleaned up on restore.

---

### 2026-10-26 — Removed the legacy TypeScript control plane

`apps/controlplane` was deleted. The Go implementation in `apps/server` is the only control plane: it has HTTPS client API, access-code pairing/certificate pinning, instance/session/admin/audit/transport, worker lifecycle, and image capture/promote. No build, runtime, or script path referenced the TypeScript tree; the only remaining references are historical decisions/plans. The Makefile now builds/runs `mako-controlplane`/`mako-worker` instead of the old `npm` targets. The former `apps/admin` was moved to `apps/server/adminui`, redesigned, and embedded into the control plane at `/admin/`.

---

### 2026-10-26 — Unified access codes; owner console needs no token

Pairing codes and enrollment codes were merged into one **access code** with a nullable `target_user_id`:

- `target_user_id = NULL`: an admin-issued code. Redemption decides from the username: if the account exists, verify its password and add this device; if not, create a new ordinary account. It can never create an admin.
- `target_user_id = <user>`: an ordinary user's self code, usable only for that account.
- Ordinary users can only create self codes; admins create unbound codes; the owner console can target any account or leave it unbound. The generator does not choose a target in the client UI; the server derives it from the caller's role.
- Codes are single-use, hashed at rest, TTL-bounded, and rate-limited by failed-password attempts. Client-side redemption still requires the username/password (or the new password when creating), so a leaked code alone cannot take over an existing account.

The client now treats `3000` as the **client-available API** (ordinary and admin users, access-code creation/redeem) and `3001` as the **owner console**. `3001` is loopback-only; reaching it is the owner proof, so `/api/v1/admin/*` accepts tokenless requests attributed to `_SYSTEM`, while a bearer token is still accepted for programmatic clients. This entry originally kept token-protected admin routes and `/api/v1/owner/*`; the later 2026-10-27 entry migrated all UI/admin routes to `/api/v1/admin/*` with no login.

The client trusts a server by `serverId` + the certificate pin in that server's profile, not by URL. `GET /server/info` is fetched with an unpinned temporary client, but trust is only granted if the certificate observed on that connection matches a profile for the returned server ID. Known servers skip the code; forgetting a server removes the profile and its pin, so re-adding needs a fresh code. Clearing app data does the same. There is no URL pin or separate server-pin store.

The startup `MAKO_PAIRING_PIN`/generated PIN is now seeded as a one-use unbound access code and logged as `bootstrap access code`. The old `/api/v1/pairing/verify` and `/api/v1/admin/pairing-codes` routes were removed.

---

### 2026-10-23 — Client design language and connection/account model

This entry's pairing-code details were superseded by the 2026-10-26 access-code model; the visual/account structure remains.

The client screens were visually inconsistent (teal seed theme, mixed app bars, cards, FABs, banners). They now share one theme: black/white surfaces, a single Sri Lankan cat's-eye gold accent, flat app bars, 14px cards, and no colored decorative blocks. Login, register, server picker, instance list, and settings use the same spacing, type scale, inputs, and buttons.

Connection identity is **(serverId, userId)**, not server alone. A server card groups its accounts as rows; one phone can keep different accounts on the same server, each with its own token, while sharing the certificate pin. "Add account" on a server opens the same flow with the URL pre-filled and pairing skipped.

The add-server flow was collapsed from four concepts (pair / invite / register / login) into one page:

1. URL.
2. Pairing code only when that URL has no pinned certificate; it is submitted together with the first login.
3. Username/password and Log in. There is no client-side registration: accounts are created by an admin, so the client never has to explain invite codes or ask the user to pair before registering.

The separate "Pair server" button and the standalone Register screen are gone. The server picker is the root screen even with zero saved servers, so the initial page already runs mDNS discovery and shows Nearby servers; instance lists are pushed on top, so Back returns to the server picker. New Instance moved to the top `+` action, matching the server screen.

Cancelling the add flow no longer removes a Nearby entry: it is only removed after a profile for that server ID actually exists. "Forget server" and "Forget all servers" remove profiles/tokens and their certificate pin; re-adding requires a fresh access code. (Superseded the earlier keep-pins-on-forget behavior.)

For users who cannot touch the host, an authenticated admin could mint a short-lived single-use pairing code (this mechanism was superseded by the unified access-code model on 2026-10-26).

```
POST /api/v1/admin/pairing-codes
Authorization: Bearer <admin token>
{"ttlSeconds": 600}
```

The code is entered as the client's pairing code. The startup `MAKO_PAIRING_PIN` remained available for local bootstrap. (Superseded by the 2026-10-26 unified access-code model.)

---

### 2026-10-23 — Multi-server client, stable server identity, mDNS discovery design

The Android client used to hold exactly one server URL/token, so switching hosts meant logging out and losing the previous pairing. It now stores a list of `ServerProfile`s (server ID, display name, base URL, certificate SHA-256, bearer token, username) in secure storage. Startup lands on a server picker; selecting one validates its token and opens that server's instance list. The instance screen's action returns to the picker without forgetting servers. A profile is keyed by the stable server ID, not by address or name, so renaming/re-addressing does not duplicate it.

The control plane now owns a stable identity: `MAKO_SERVER_ID_PATH` (default `<db>.server-id`) persists a UUID across restarts, `MAKO_SERVER_NAME` is the configurable display name (default hostname), and unauthenticated `GET /api/v1/server/info` returns `{serverId,name,version}`. The client records that identity when adding a server and refreshes the display name on selection.

LAN discovery is implemented for both sides, following [`mdns-discovery.md`](mdns-discovery.md): the control plane advertises `_mako._tcp.local` with TXT `id`, `name`, `v`, and `path` on private LAN interfaces; the Android server picker browses it via `NsdManager`, deduplicates by server ID, shows unknown servers under "Nearby", and updates a known server's address/name only after verifying `/server/info` over the pinned certificate. mDNS remains only an address hint — certificate pinning/access-code trust bootstrap are still mandatory. Manual URL entry remains the fallback. `MAKO_DISABLE_MDNS=1` turns advertisement off.

---

### 2026-10-22 — PIN-gated certificate pinning for the Android client

This entry is historical; the pairing endpoints described here were replaced by the unified access-code model on 2026-10-26. The certificate-pinning principle remains.

The client API now has a working pairing flow. The control plane generates a six-digit startup PIN (or uses `MAKO_PAIRING_PIN`) and an authenticated admin can mint short-lived single-use pairing codes via `POST /api/v1/admin/pairing-codes`. Both are printed/returned with the server certificate SHA-256, and `POST /api/v1/pairing/verify` is constant-time and rate-limited to five failures/minute. The Android add-server page shows a pairing-code field only until the URL's certificate is pinned; it submits the code together with the first login, checks that the fingerprint returned by the server matches the certificate presented on that TLS connection, and stores the fingerprint in `flutter_secure_storage`. All later API clients pin that fingerprint and reject a different certificate.

This is PIN-gated TOFU, not a PAKE: an active MITM during the very first pairing could still substitute its certificate. It removes blind trust-on-first-use and passive interception; a PAKE/SAS is future work. Authorization remains bearer-token based — pairing establishes TLS trust, not user identity.

Also added `FLAG_KEEP_SCREEN_ON` for the full-screen stream (cleared on exit), and the test Tone process was stopped. Hardware volume keys now control the client's media stream (AndroidAudioConfiguration.media), which is the intended behavior since the guest has no convenient volume UI.

---

### 2026-10-22 — One worker per instance; headless is the worker deployment default

The product topology is **one worker process per instance** (the control plane spawns a worker per instance and each worker owns one emulator). That makes headless a deployment policy rather than a per-client choice: `MAKO_HEADLESS` defaults to `1`, and `androidstudio.Adapter.Start` overrides `LaunchRequest.Headless` from config. `MAKO_HEADLESS=0` exists only for local debugging with a visible emulator.

Verified on the real phone against a headless instance: qemu ran as `qemu-system-x86_64-headless.exe` with `-no-window` and no emulator window; video stayed ~30fps (`framesDecoded` tracking), audio ran at ~50 packets/s with non-zero level, `getevent` saw touch events, and the guest was switched to three-button navigation. The client's `launchConfig.headless` is now true but the worker is authoritative.

Headless initially still played the guest's tone on the Windows host speakers while streaming. Fixed by launching the emulator with `-audio none` (`MAKO_EMULATOR_AUDIO`, default `none`): the guest audio graph and gRPC `streamAudio` still work (host probe decoded 440Hz/RMS≈164), but the emulator has no host audio backend, so the host is silent. `MAKO_EMULATOR_AUDIO=auto` restores host sound for local debugging.

Still open for server deployment: running the worker under a Windows service/Session 0 may lose the interactive GPU/audio device; test a logon-triggered scheduled task first, then a service with software-GPU fallback. The quickboot stale-audio issue also remains: a stop/start can restore a muted guest, so cold-boot if audio is silent.

---

### 2026-10-22 — Full-screen stream with guest-native navigation, phone Back exits

The phone's own Back/Home gestures cannot be forwarded to the guest (Android consumes them before the app sees them). Earlier client overlays (status pill, Exit button, floating ball, bottom control bar) duplicated the guest's controls and the floating ball overlapped the guest's Home button. The final simpler design:

- The client hides the phone's status/nav bars through a small `mako/window` MethodChannel (Flutter's `immersiveSticky` still left the status bar visible on MIUI).
- The worker switches the guest to **three-button navigation** via `cmd overlay enable-exclusive --category com.android.internal.systemui.navbar.threebutton` when handling an offer. The guest's Back/Home/Recents buttons are part of the video and are touchable, so there is no client-side navigation overlay.
- The phone's Back gesture is the only client-side exit: `PopScope` disconnects the attachment and returns to the instance list.

Verified on the real phone: full-screen video with no phone status/nav bars, guest three-button nav at the bottom, phone Back returned to the instance list and restored the phone's system bars. This also removes the overlay touch pass-through/coordinate problems entirely.

---

### 2026-10-22 — Android client plays stream audio as media, not a phone call

The real phone initially treated the WebRTC audio track as `USAGE_VOICE_COMMUNICATION`: it came out of the earpiece and the volume keys adjusted call volume. Fixed by calling `WebRTC.initialize(options: {'androidAudioConfiguration': AndroidAudioConfiguration.media.toMap()})` before the first peer connection. flutter_webrtc applies those attributes while building its `JavaAudioDeviceModule`, so this must happen at startup, not per connection.

Verified on the Xiaomi phone: the active track's `Usg` became `1` (media), the route is `speaker`, and Volume Up changed `STREAM_MUSIC` 47→57 while `STREAM_VOICE_CALL` stayed at 11. The stream audio is PCMU; this only changes Android's usage/route/volume category.

---

### 2026-10-22 — Silent audio was a stale emulator quickboot state, not WebRTC port muxing

A real phone received audio packets but `audioLevel` stayed 0. A host-side probe decoded the worker's PCMU output as digital silence (`rms=0`, `peak=0`), and the phone's own WebRTC `AudioTrack` was active but writing zeros. So the fault was upstream of ICE/RTP, not "audio and video share one UDP port".

Added `MAKO_AUDIO_DEBUG=1`, which logs each `streamAudio` packet's size and S16 RMS/peak plus `WriteSample` stalls. Against the broken instance, `streamAudio` delivered **136-byte packets at ~93/s with RMS ≈1.5 and peak 2** (near-silence dither), so the PCMU encoder produced only ~3 frames/s. After a guest `adb reboot` (cold boot, not an emulator stop/start that restores the quickboot snapshot) and a fresh Tone `AudioTrack`, the same worker logged **1920-byte packets at ~95/s, RMS ≈192, peak 271**, i.e. ~50 PCMU frames/s. The real phone then went from ~3 to ~50 audio packets/s and `audioLevel=0.0084`.

Root cause: the emulator's quickboot snapshot restored a wedged guest audio state (stale `AudioTrack`/AudioFlinger output), so the emulator produced tiny silence fragments. WebRTC BUNDLE, the single UDP port, and headless mode were all irrelevant. Product follow-up: instances should cold-boot (disable snapshot load) or detect this state before restarting, because a stop/start can re-restore the same broken snapshot.

---

### 2026-10-22 — ICE advertises all routable IPv4; no Tailscale-specific config

An earlier attempt at the same date restricted ICE to one detected `192.168/10/172` address to stop an Android emulator client from selecting a Tailscale candidate through a Windows NAT hairpin. That fixed a test-environment symptom by breaking a real deployment: a phone on Tailscale only saw an unreachable LAN candidate, and the worker's UDP socket also only listened on that address. Special-casing Tailscale would repeat the same mistake one layer up.

The policy is now: IPv4/UDP only, advertise every routable IPv4 interface (LAN, Tailscale `100.64/10`, other VPNs), exclude link-local/multicast/IPv6 (loopback stays for local probes), and let ICE choose. `MAKO_WEBRTC_HOST_IP` remains as a debug override for a specific interface, not as a Tailscale prerequisite. `host.IsAdvertisableIPv4` owns the predicate and is unit-tested for LAN, Tailscale, link-local, multicast and IPv6.

Verified on a real phone (Xiaomi 23127PN0CC, Wi-Fi `192.168.1.4`): ICE selected `192.168.1.4 -> 192.168.1.7`, video ran at ~30fps with `framesDecoded` tracking `framesReceived` and no freeze over the test window. The emulator-as-client hairpin remains a test-environment artifact and is not a reason to restrict candidates.

---

### 2026-10-22 — Audio (PCMU) and multi-touch (gRPC sendTouch) land on Windows

**Audio path.** The emulator's `streamAudio` RPC is now in the vendored proto and `internal/worker/transport/emugrpc/audio.go` streams S16LE packets (requested 48 kHz stereo). `webrtc/audio.go` downmixes to mono, linearly resamples to 8 kHz, μ-law-encodes, and writes 20 ms (160-byte) samples to a WebRTC audio track. The codec is deliberately **PCMU, not Opus**: Opus needs either cgo/libopus or a hand-written encoder, PCMU is pure Go and universally supported, and 8 kHz mono is enough to prove the path. A real 440 Hz tone played in the guest was captured at exactly 440.0 Hz (7 s, 56,000 bytes, RMS 192) — not silence.

The Android client now adds a recv-only audio transceiver (without it the worker's audio track never negotiates) and logs inbound audio stats (`packetsReceived`, `audioLevel`). It auto-plays the remote track via flutter_webrtc.

**Touch path.** `sendTouch` is called through `emugrpc/input.go` instead of spawning one `adb shell input` process per event. `transport.InputEvent` carries `pointerId`; the client forwards Flutter's `event.pointer`; pressure is 1 for down/move and 0 for up (the emulator unregisters an identifier on pressure 0). adb remains the fallback for key events and for emulators without `sendTouch`. Verified at the kernel level with `getevent`: two simultaneous `ABS_MT_SLOT`s with tracking IDs 0/1, independent moves, then both lifted.

**Bugs this surfaced:**
- The PCMU resampler could compute a drop index a few samples past its buffer (packet sizes are not multiples of the 48k→8k ratio) and panicked the worker. Fixed by clamping, with a regression test using emulator-sized (1088-frame) packets.
- AMF `-usage lowlatency` emits **one IDR ever** (measured 1 IDR in 5 s with `-g 30`), so a client that loses any reference frame freezes permanently. Switched to `lowlatency_high_quality` + `-header_spacing <gop>`; AMF now emits an IDR and SPS/PPS every second. This is why a real client could freeze while the RTP stream kept flowing.
- The host has several virtual adapters (Tailscale 100.64/10, Hyper-V, link-local, IPv6); advertising all of them let ICE pick a lossy path on the same-host emulator. The final policy is to advertise all routable IPv4 (Tailscale included) and let ICE choose; an intermediate attempt to filter to one LAN IP was reverted the same day because it broke Tailscale clients. `MAKO_STREAM_BITRATE` makes the H.264 bitrate tunable for weak links.

**Still open:** an Android emulator acting as the client on the same Windows host goes through a Windows NAT hairpin to the host's own LAN IP and loses packets under video load (host-side probe on the same worker receives a steady 30 fps). Audio and touch work through that path; video decodes initially and then stalls on lost keyframe packets. The trustworthy end-to-end video check is a physical phone on the LAN, which has not been available in this session.


### 2026-10-22 — Windows host support, multi-instance per owner, and two real multi-instance bugs

**Windows host.** The server previously only built on macOS/POSIX. Making Windows a first-class host required four changes, each verified on a real Ryzen 9700X / RX 9070 / Windows 11 box:

- `bootstrapsecret` split into platform files: Unix keeps `O_NOFOLLOW` + exact `0600` mode check; Windows rejects reparse points via `GetFileAttributes` and relies on the per-user NTFS ACL (POSIX mode bits are synthesized there, so the old check made `/auth/setup` fail with mode 666).
- Emulator liveness/stop split into platform files: Windows uses `OpenProcess`/`GetExitCodeProcess` and `taskkill /T /F`, because Windows has no signal-0 or `ps`. Instance stop now first asks the emulator to shut down through `adb -s emulator-<consolePort> emu kill` on every platform, waits up to 15s, and only then falls back to the process kill — this is what makes stop work at all on Windows, and it gives Unix a cleaner shutdown than bare SIGTERM.
- Emulator/`qemu-img`/worker executable resolution is platform-aware (`.exe`, Windows SDK paths, `MAKO_WORKER_BINARY_PATH` default), and `DiscoverTemplates` respects `ANDROID_AVD_HOME`.
- `STUN_URL=none` disables STUN explicitly (the historical default is a public Google STUN server, which is unnecessary on a LAN and often unreachable).

The full Go test suite now passes natively on Windows (with POSIX-only tests skipped or build-tagged). End-to-end on this host: controlplane + two concurrent emulator instances + Flutter Android client, with the client reporting `framesDecoded` tracking `framesReceived` at 30fps (540x1200, decode ~5ms).

**AMD AMF probe false negative.** `h264_amf` encodes real 540x1200 frames fine on the RX 9070, but the old `RealEncoderProber` used a hardcoded 64x64 probe; AMF's `encoder->Init()` fails at that size, so `SelectEncoder` silently fell through to `libx264`. `EncoderProber.Probe` now takes the real capture dimensions, and the worker logs the chosen encoder. Verified: logs show `selected encoder h264_amf`, and the client decodes the resulting stream. The `-bf 0` flag was also removed from the AMF branch — AMF ignores it with a noisy "Maxium number of B frames needs to be specified." warning even though the encode succeeds.

**Multi-instance per owner.** The original model allowed exactly one instance per user (`instances_unique_owner`, migration 0011). For an open-source product that is the wrong constraint: one user should be able to create several instances from a template and connect to whichever is running. Migration `0014` drops the unique index and adds a plain `(owner_id)` index; `ErrOwnerConflict`/409 were removed. `GET /api/v1/templates` was added so the client can show a real template picker instead of asking for a typed template id.

**Bug 1: per-instance cloned AVDs appeared as templates.** `DiscoverTemplates` scanned every `.ini` in `~/.android/avd`, including mako's own `mako_instance_*` clones, so after starting one instance it showed up as a selectable template (and could be cloned again). Discovery now skips the reserved `mako_instance_` prefix.

**Bug 2: two concurrent starts got the same emulator ports.** Every worker process started its console/adb/grpc search at 5600/5601/8554, so a second instance raced the first for the same ports (observed: both handles reported console 5600; only one emulator survived). The control plane now allocates the triple (`MAKO_EMULATOR_PORTS=console,adb,grpc`) under a mutex before spawning, checking real TCP availability; the worker honors the assignment. Re-verified with two instances on distinct ports (5604/5606) both streaming simultaneously.

**Bug 3: multi-slice H.264 inflated RTP timestamps.** `pipeH264` treated every slice NAL as a complete access unit. `libx264 -tune zerolatency` emits one slice per CPU core (16 on this host), so each picture became 16 RTP samples and timestamps advanced ~16× too fast. Grouping now uses `first_mb_in_slice == 0` (and AUD NALs when present) to start a new picture; continuation slices stay in the same sample. A unit test covers the multi-slice case, and the real client now reports ~30 network/decode fps instead of hundreds.

### 2026-10-20 — Base image capture/promote (Phase 6) built; live boot verification unresolved


`internal/controlplane/image` (capture-candidate/promote/resolve), the `templates`/`base_images` tables (migration `0013`), and the routes designed in `planned-subsystems.md` all landed and pass unit tests — see `data-model.md`'s "What `0013` did" for the schema and mechanism.

**What's confirmed working, live, not just unit-tested:** per-instance AVD cloning for ordinary instance isolation (unrelated templates/base-image work, shipped earlier the same day) continued working reliably throughout this entire investigation — dozens of plain template → new-instance boots succeeded normally. Deleting an instance correctly removes its cloned AVD (`CleanupInstanceData`), confirmed via a real delete mid-investigation.

**What's unresolved:** a new instance seeded from a *captured and promoted base image* hung during boot in 3 out of 3 attempts (all threads idle per `sample`, not a CPU-bound fsck or memory-pressure symptom — this was checked directly, not assumed). One isolated, minimal manual reproduction (a hand-built AVD clone, single write-sync-SIGTERM-`qemu-img commit`-reboot cycle, no mako code involved) *did* correctly show a post-capture file after reboot, proving the core idea (merge the emulator's `userdata-qemu.img.qcow2` quickboot overlay into its backing file via `qemu-img commit` before copying `userdata-qemu.img` out — the emulator stops writing new guest state directly into that file after first boot, confirmed by direct testing) is sound in principle. But the 3 live failures were all against a real `mako-controlplane`/`mako-worker`-driven capture of an instance that had been stopped via `StopProcess`'s bare `SIGTERM` — the leading theory is that an unclean guest shutdown leaves the captured filesystem in a state that triggers a very slow (or hung) recovery pass on the *next* cold boot, worse for larger/more-used images than the minimal manual test's near-empty one. Low free host memory during testing (~1GB of 64GB after an extended dev session) is a confounder that wasn't fully ruled out either.

**Before trusting this feature in production**, try: (1) a graceful guest shutdown before capture (`adb shell reboot -p` or the emulator console's shutdown command, not a bare process `SIGTERM`) so the filesystem captured is clean, not mid-write; (2) re-run the exact capture → promote → new-instance-boot sequence on a freshly-rebooted host to eliminate the memory-pressure confounder entirely; (3) if it still hangs, bisect by capturing a *freshly-created, never-really-used* instance (closer to the minimal manual repro that worked) versus a heavily-used one, to see if captured-state size/complexity is the actual variable. Don't mark this phase done in `roadmap.md` until one of these actually boots successfully end-to-end.

**Update, same day, follow-up investigation:** re-ran the bisection above. A minimal-usage capture (stop immediately after first boot, no marker-writing, different template) still hung identically — ruling out "captured-state size/complexity" as the variable (its overlay was ~1GB too, apparently just from normal first-boot system churn, not from anything the test did). The decisive test: booted a **plain, no-base-image-involved** instance of a template never touched by this investigation, as a control. It took 90+ seconds and climbing (actively burning CPU, not idle) before being killed, against an established ~35-second baseline measured earlier the same session for an identical kind of boot. This points at **host-level degradation after an extremely long dev session (many hours, dozens of emulator launches)** as the dominant factor — not a defect in the capture/merge code specifically. The earlier "3/3 hangs, all threads idle" observation may simply have been this same degradation manifesting worse under the extra I/O load of booting from a freshly-copied large image, rather than a distinct capture-specific failure mode. **Recommendation: redo this entire verification after a clean host restart before drawing any firm conclusion** — this session's host state is no longer a trustworthy test environment for *any* emulator-boot timing, capture-related or not.

### 2026-10-16 — Worker lifecycle (Phase 4) built, plus a real-spawn bug no unit test could catch

Real `exec.Command` worker spawning landed: `internal/controlplane/workerproc` (OS-process mechanics, fake-able `Execer`), `placement.Selector.AcquireWorker`/`ReleaseWorker` (port allocation + spawn/kill, built on `workerproc`), `instance.Service` and `transport.Service` restructured so every worker interaction resolves a client scoped to one spawned-on-demand process instead of a single shared static `MAKO_WORKER_BASE_URL`, and `reconcile.WorkerLivenessProbe` (health-checks each running instance's worker, respawns + adopts the still-running emulator on failure via a new worker-binary route, `POST /v1/instances/{id}/adopt`). See `docs/plans/worker-lifecycle.md` for the full design.

One addition wasn't in the original design docs: the worker-side "reconcile with the still-running emulator by known console/adb port" capability didn't actually exist anywhere — `runtime.Registry.Reconcile()` only re-validates handles already in its own in-memory store, it can't discover an emulator a fresh replacement process never launched. Added `Registry.Adopt(handle)` (insert a caller-supplied handle directly, no launch) plus the `/adopt` route, so a respawned worker can be told "here's the emulator you're inheriting" using exactly the fields already in `runtime.InstanceHandle`.

**The bug worth recording:** all of this passed every unit test (fakes throughout, by design — see the testing bar below) but failed on the very first real spawn against the real `mako-worker` binary. `placement.Selector.AcquireWorker` called `workerproc.Spawn(..., nil)` for `extraEnv`, and `workerproc.Spawn` builds the child's env as `append(extraEnv, "MAKO_WORKER_LISTEN_ADDR=...", ...)` — passed straight into `exec.Cmd.Env`, which **replaces the child's entire environment** when non-nil (Go's documented behavior, not a bug in that package). With `extraEnv == nil`, every spawned `mako-worker` started with *only* those three `MAKO_WORKER_*` vars — no `PATH`, no `HOME`, no `MAKO_ANDROID_SDK_ROOT` — so it could discover zero templates and `CreateManagedInstance` failed immediately with "runtime template not found," with no unit test able to catch it since every fake `Execer` ignores env passthrough entirely (fakes answer HTTP directly, they never consult `PATH`/`ANDROID_SDK_ROOT` to find a real emulator binary). Fixed by passing `os.Environ()` as `AcquireWorker`'s base env, with the three `MAKO_WORKER_*` overrides appended after (last-value-wins on key collision, so they still take precedence). Verified by rebuilding both binaries and running the full create → start → kill-worker → respawn-and-adopt → stop cycle against a real `mako-worker` process and a real Android Studio Emulator (`Medium_Phone_API_36.1`) — confirmed via `ps` that the emulator process survives the worker's death untouched (same PID/ports before and after respawn), and via the `instances.runtime_handle_json` diff (`workerPid`/`workerBaseUrl` changed, nothing else did).

**Lesson for future work in this codebase:** a fake-`Execer`-based unit test proves the *call sequence* is right (spawn → wait-healthy → use → release), not that the *spawned process can actually do its job* — those are different failure classes, and only the latter requires the one real end-to-end run this project's testing bar already asks for (`decisions-log.md`'s "one integration test spawning a real worker process"). Don't skip that step for future `exec.Command`-based work just because the unit tests are green.

### 2026-10-15 — Why mako exists: the problem desktop-streaming tools can't solve

Recorded because it was previously undocumented anywhere, and it reframes how to evaluate this project against superficially-similar tools (general remote-desktop apps, cloud-gaming platforms, other WebRTC streaming clients).

The project's origin: using Artemis+Apollo (a Moonlight/Sunshine-family streaming pair) to play touch-native mobile games on a phone, streamed from a PC. Two problems turned out to be architectural, not implementation gaps in Apollo:

1. **No real touch semantics.** Desktop OSes have no native touchscreen concept. Apollo can only translate touch gestures into a virtual gamepad or mouse — there's no startup flag or config that makes the *server* side understand touch, because the server is streaming a desktop session that was never touch-aware to begin with.
2. **Single input/output per host, no real multi-instance concurrency.** A physical machine's keyboard/mouse/display are exclusive resources. Two clients can never each independently drive two separate "instances" running on the same host — that's the Windows/macOS multi-seat-input problem (notoriously hard to solve well), not something Apollo left unimplemented.

An Android emulator is touch-first by construction, and running several independent emulator instances on one host is already an ordinary capability (Android Studio runs multiple AVDs in parallel routinely, each with its own isolated input/output). Wrapping each instance's full audio/video/touch I/O in WebRTC sidesteps the multi-seat problem at the root instead of patching around it at the desktop-streaming layer.

**Consequence for how to evaluate this project:** mako is not a general remote-desktop or cloud-gaming competitor. A comparison against things like 网易UU远程 (NetEase-backed, general remote-control + cloud-GPU-rental platform with years of infrastructure investment) or `cloudplayplus_stone`-style Flutter/WebRTC desktop-streaming clients is the wrong lens — both of those are still desktop-streaming-paradigm tools, subject to the same two limits above. Nothing surveyed (Apollo/Sunshine/Moonlight/Artemis, 网易UU远程, `cloudplayplus_stone`, `jacobbo/remote-android`, Google's own `android-emulator-webrtc`/Cuttlefish WebRTC) combines mako's actual scope: self-hosted, Android-*emulator*-specific (not phone mirroring, not desktop streaming), native touch I/O, true per-instance isolation for concurrent multi-user access, versioned base images, and a native-client access-code/certificate-pinning trust model (`security-model.md`) instead of an account+relay-server model. This doesn't make mako's engineering easier or guarantee it's worth continuing on its own — it just means the right question is "does it solve the touch-semantics + multi-instance-concurrency problem well for its actual users," not "can it beat a funded cloud-gaming incumbent at streaming quality."

### 2026-10-14 — Session invites redeem by 6-digit PIN, not an opaque token

TS's session-invite flow issued the same 43-character base64url token shape used for bearer credentials. Revisited while porting: an invite is meant to be read aloud or typed by someone standing nearby, not pasted from a link, so it was redesigned around a short-lived numeric PIN instead. Decided explicitly with the user (not unilaterally):

- **6-digit numeric PIN** (1,000,000 combinations), not alphanumeric — optimizes for "dictate it to a friend," matching Zoom/Meet/WiFi-share conventions.
- **Fixed 5-minute TTL**, no longer a caller-configurable 1–60 minute range — a PIN's much smaller space makes the window part of the safety margin, not just a UX knob.
- **Global lookup, no instance/session id in the redeem request** — `host:port/join/<pin>` is the whole link. At this project's scale, a collision between two simultaneously-active invites in a 1-in-a-million space is a non-issue, and it's the shortest possible link.
- **Guest redemption still creates a lightweight `users` row** (`user_type = 'guest'`, display name only), exactly like TS's `createGuestUser` — a fully stateless alternative (name stored directly on the attachment, no user row at all) was considered and explicitly declined for this pass.
- PIN hashes reuse `auth.HashToken` (unsalted SHA-256), same as every other credential in this schema, accepting that a 6-digit space is cheaply precomputable by anyone with DB read access. Not treated as a gap worth a peppered-hash scheme here — DB read access already exposes far more.
- **No brute-force rate-limiting/attempt-cap added.** Flagged as a deliberately deferred hardening item (nothing else in this port rate-limits a redemption endpoint either), not an oversight — revisit before this leaves a trusted-LAN deployment model.

No migration needed: `session_invites.token_hash` already stores an opaque hash with no length/format assumption anywhere in the schema or queries. See `docs/plans/session-transport-redesign.md`'s "Session invite redesign" section for the full writeup.

### 2026-10-14 — Drop the `workers` and `settings` tables (decided; migration not yet written)

While scoping the instance/placement phase, re-examined whether a persisted `workers` registry (added `0009`, unused by any code) was actually needed. The original justification was a shared pool of long-lived local worker processes requiring health-based placement selection and restart affinity. Two things undermined that:

1. `internal/worker/runtime/registry.go`'s `handles map[string]InstanceHandle` shows a single `mako-worker` process already multiplexes multiple concurrent instances internally — "multiple local workers" was never a hard technical requirement, just a carried-over assumption.
2. The better MVP model is **one worker process spawned on demand per running instance** (`exec.Command`, torn down when the instance stops), not a shared pool. Under that model there is no placement *selection* problem (nothing to choose among — every instance start spawns its own fresh worker) and no *affinity* problem (an instance's durable state lives in its qcow2 overlay on disk, not in worker-process memory, so there's nothing to preserve across a respawn). It also strictly improves fault isolation versus any shared-worker arrangement: a worker crash can only ever affect the one instance it was spawned for.

Consequence: no `workers` table, no `instances.worker_id` column. Whichever process is currently serving an instance is runtime status, not durable config, and belongs in the already-existing `instances.observed_state`/`runtime_handle_json` fields (already a spec/status split) — zero new schema for worker tracking. A `workers` table would become justified again only if a pooled/remote worker legitimately serving multiple instances is introduced later; see `planned-subsystems.md`'s worker-lifecycle section for the current (revised) design target.

Separately, confirmed `settings` (generic key/value table) has zero callers anywhere in the (now removed) TypeScript tree — its `putSetting`/`getSetting` were dead code, same speculative-and-unused pattern as `workers`. Dropping both.

Neither drop has been executed as a migration yet as of this writing — `data-model.md` still lists both tables as physically present (truthful to the migrated schema) with a pointer back to this entry. Update both files together once the removal migration lands.

### 2026-10-13 — Go bumped to 1.27.2; superpowers methodology and artifacts retired

`apps/server/go.mod` moved from `go 1.26.0` to `go 1.27.2` to match the locally installed toolchain. This is a personal, pre-release, unreleased repository with no deployed users — there's no reason to lag behind the installed compiler, and the bump is a no-op risk-wise (verified: `go build ./...` and `go test ./...` both pass unchanged after the bump).

Simultaneously, the "superpowers" spec/plan-writing methodology was dropped from use, and `docs/superpowers/` (specs + plans) and the repo-root `.superpowers/` (subagent-driven-development scratch: progress files, review diffs) were deleted. Both directories were already `.gitignore`d and untracked, so deletion didn't touch git history. Everything from those documents still relevant to understanding the system today was extracted into this `docs/architecture/` folder before deletion — nothing was lost, but there is no longer a historical trail to cite; decisions are recorded here with full inline rationale instead of citations.

### Earlier (introduced during the auth port, prior to 2026-10-13)

**`apiserver/commonauth` package.** Login, `/auth/me`, and logout need to be registered identically on both the admin and client HTTP surfaces, but `adminapi` and `clientapi` are sibling packages that must not import each other (keeping the loopback/LAN network boundary meaningful depends on them staying independent). A third, small shared package (`commonauth`) holds those three handlers once, imported by both surfaces, instead of duplicating the handler code or collapsing the surfaces together.

**Migration `0010`: dropped `users.is_server_owner` and `users.enrollment_required`; renamed `pairing_invites` → `registration_invites`; dropped the `EnrollLegacy` method/route.** `is_server_owner` and `enrollment_required` were vestiges of an earlier single-admin "Server-owner" identity model, superseded by the current multi-admin `user_type` model — neither column was read by any surviving code path, and `EnrollLegacy` existed only to migrate old passwordless "Principal" accounts into that superseded model. Since this is a pre-release project with no such accounts to migrate, the whole bridge was deleted rather than ported. `pairing_invites` was renamed because the actual domain concept is called a "registration invite" everywhere else in the code (`CreateRegistrationInvite`, the `registration_invites` table itself) — `pairing_invites` was a naming leftover nothing else used. The client-facing `POST /api/v1/auth/pairing-invites` and `.../redeem` routes were kept as `410 Gone` tombstones rather than deleted outright, so a stale caller gets an explicit "retired" response instead of an ambiguous 404.

### 2026-10-10 — No browser-based streaming client, ever

Design iterations from mid-2026 built a real browser WebRTC client: a React/antd admin UI with a live `<video>` session page, SDP/ICE relay through the control plane, pion-based WebRTC in the worker. Over successive iterations that page was progressively demoted — first to "internal diagnostic, not a product entry point" (no further investment in its mDNS/ICE interoperability issues), then to "development-only, unavailable by default in production" — before being dropped entirely: the only client that will ever establish a streaming session against a worker is a future native `mako-client` app, connecting over a trusted LAN. There is no plan to revive a browser client. Treat any TS code implementing the old `Session.tsx` browser page as dead weight when porting, not as something to carry into the Go control plane.
