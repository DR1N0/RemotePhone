# RemotePhone

**Status: paused indefinitely · v0.0.1 · a finished experiment, not a product**

[Release notes](docs/releases/v0.0.1.md) · [GitHub release](https://github.com/DR1N0/RemotePhone/releases/tag/v0.0.1) · [中文](README.zh-CN.md)

RemotePhone started as a simple idea: run several Android emulators on my own PC, stream each one to a phone with real touch input, and let a few people each have their own "phone" on the same machine.

It got surprisingly far. Then it ran into miHoYo's anti-cheat and stopped there.

This repository is the honest snapshot of that attempt: the control plane works, the client works, the streaming/touch/orientation plumbing works — but the one use case that made the project interesting, playing games like **Zenless Zone Zero** and **Genshin Impact** through an Android emulator, does not work, and I do not have a responsible way around it.

---

### What this was supposed to be

A self-hosted, LAN-first alternative to desktop streaming for **touch-native mobile games**:

- every user gets a real Android emulator instance, not a desktop stream with a fake controller overlay;
- each instance has its own AVD, userdata, worker process, and WebRTC stream;
- instances are created from captured base images, with thin QCOW2 overlays so a 30 GB game does not have to be cloned per user;
- the phone client sends native touch/key input directly to the emulator;
- no cloud account, no relay server, no per-minute billing.

That was the differentiator. A generic PC-streaming box would be competing with Sunshine/Moonlight, and I had no reason to do that.

### Why it stopped

**Zenless Zone Zero's anti-cheat crashes inside the emulator's ARM translation layer.**

Concretely:

- the game ships `arm64-v8a` native libraries only;
- the Android Studio x86_64 emulator runs them through `libndk_translation` / berberis;
- after login, the game loads `libtersafe2.so` (Tencent anti-cheat), and the process dies with:

```text
Fatal signal 6 (SIGABRT)
Abort message: 'Guest call didn't restore sp: expected ..., actual ... -0x10'
#04 libndk_translation.so (berberis::ExecuteGuestCall)
```

I reproduced this consistently. Clearing the Vulkan PSO cache did not help; it only made the game download about 261 MB of data again. Switching encoders, restarting the game, and clearing the cache all left the same native abort. This is not a rendering bug in RemotePhone; it is the anti-cheat interacting with the x86-to-ARM translation boundary.

The same category of problem applies to other heavy miHoYo titles. The old "PC client in mobile/touch mode" launch flags for Genshin/Star Rail were unofficial and got removed; a quick scan of the local Zenless Zone Zero PC install found no mobile UI/touch mode to switch on. A PC client with a virtual controller is a different product, and Sunshine/Moonlight already does PC streaming very well.

So the project lost the thing it was built around: **native touch + independent Android instances**. A LAN streaming server that cannot run the games in question is interesting engineering, but not a product.

### What actually works

This is why the repo is still useful as a snapshot:

- Go control plane and per-instance worker processes;
- user accounts, unified access codes, certificate pinning, loopback owner console;
- mDNS server discovery;
- Android emulator instance lifecycle (create/start/stop/delete/adopt);
- QCOW2 thin base-image layering, including the encryption-key handoff;
- WebRTC H.264 video and PCMU audio;
- native multi-touch and key input;
- landscape/portrait capture normalization and touch-coordinate mapping;
- a collapsible edge menu with guest Back/Home and stream exit;
- a Flutter Android client and an embedded React owner console;
- tests for the control plane, worker, capture/transport, and auth paths.

It can be used for **light emulator-friendly Android apps**. It was never hardened for production, multiple concurrent users, or untrusted networks.

### What does not work / was never finished

- **Zenless Zone Zero, Genshin Impact, Honkai: Star Rail** — anti-cheat + ARM translation blocker described above.
- **Production operations** — no installer, no CI release pipeline, no upgrade/migration story, no GPU capacity enforcement.
- **Base-image polish** — shader-cache bake-in with login wipe is not implemented; base-image GC and async capture/progress are still open.
- **Security hardening** — trusted LAN only; self-signed certificate; do not expose it to the public internet.
- **Cross-platform clients** — Android only; no iOS/desktop client.
- **Performance guarantees** — tested on one Windows host and one phone; not a benchmark.

### If you still want to run it

Prerequisites: Go 1.27+, Android SDK/emulator with an x86_64 image, ffmpeg with H.264 support, Windows Hypervisor Platform on Windows, and Flutter/JDK 17 for the client.

```powershell
cd apps/server
go build -o ..\..\.bin\remotephone-worker.exe ./cmd/remotephone-worker
go build -o ..\..\.bin\remotephone-controlplane.exe ./cmd/remotephone-controlplane

$env:REMOTEPHONE_DB_PATH = "E:\RemotePhone\remotephone.db"
$env:REMOTEPHONE_WORKER_BINARY_PATH = "E:\RemotePhone\remotephone-worker.exe"
$env:REMOTEPHONE_ANDROID_SDK_ROOT = "$env:LOCALAPPDATA\Android\Sdk"
$env:REMOTEPHONE_FFMPEG_PATH = "C:\ffmpeg\bin\ffmpeg.exe"
$env:REMOTEPHONE_CLIENT_ADDR = "0.0.0.0:3000"
$env:REMOTEPHONE_ADMIN_PORT = "3001"
$env:REMOTEPHONE_GPU_MODE = "host"
$env:STUN_URL = "none"
.\..\..\.bin\remotephone-controlplane.exe
```

Then open the owner console at `http://127.0.0.1:3001/admin/` and build/install the client:

```powershell
cd apps/client
flutter pub get
flutter build apk --debug --dart-define=REMOTEPHONE_DEFAULT_SERVER=https://192.168.1.10:3000
adb install -r build\app\outputs\flutter-apk\app-debug.apk
```

The Android package is `com.remotephone.client`. The client discovers servers over `_remotephone._tcp` mDNS, or you can add the URL manually. First contact uses a one-time access code and then pins the server certificate.

For the full setup, env-var table, firewall notes, and API details, see the archived documentation in [`docs/`](docs/). The README intentionally no longer claims this is an active product.

### If someone wants to continue

The useful pieces are the control plane, access-code/cert-pinning auth, QCOW2 base-image layering, and the WebRTC touch/orientation pipeline. They can be reused.

The least dishonest next paths are:

1. **real Android phones** as the runtime (native touch, native anti-cheat);
2. **Windows PC game streaming** for PC-native titles, accepting that Sunshine/Moonlight already owns that space;
3. **light/emulator-friendly Android apps** on this emulator runtime.

What I would not recommend is spending more time trying to make an Android x86 emulator pass a AAA mobile anti-cheat. That is an environment boundary, not a bug to patch in this repo.

### The one untested escape hatch: Apple Silicon

There is no official x86_64 Zenless Zone Zero APK; the shipped Android build is `arm64-v8a` only. On an **Apple Silicon Mac**, however, you can run an **arm64 Android system image** instead of translating ARM code on x86. That removes the `libndk_translation` crash path and is the only emulator route I would still consider testing.

The Go server does cross-compile for `darwin/arm64` (verified), but I have not tested the full stack on macOS, the arm64 emulator image, its GPU/performance, or whether the anti-cheat is happy on that setup. An Intel Mac repeats the x86 translation problem and is not interesting. Treat this as an experiment, not a supported path.

### License and trademarks

MIT. See [`LICENSE`](LICENSE).

RemotePhone is not affiliated with or endorsed by miHoYo/HoYoverse, Tencent, Google, or the Android Open Source Project. Game names and trademarks belong to their respective owners.

---
