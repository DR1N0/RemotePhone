# mako

Mako hosts Android emulator instances on your own machine and streams them to an Android phone or tablet over a trusted LAN. Each instance is a fully independent Android device: native touch input (not a virtual gamepad), its own cloned AVD/userdata, its own worker process, and its own WebRTC video stream.

It is built for the case desktop-streaming tools cannot express: playing touch-native mobile games on a phone, and letting several people drive several independent emulator "seats" on one host.

## Status

- **Server (Go)**: active implementation in [`apps/server`](apps/server). Auth, multi-tenant users/credentials, multi-instance-per-owner, template discovery, instance lifecycle, sessions/PIN invites, WebRTC transport, admin API, worker supervision, HTTPS on the client API, and base-image capture/promote are implemented and covered by tests.
- **Client (Flutter, Android)**: [`apps/client`](apps/client) has login/registration, a template picker, a multi-instance list (start/stop/connect/delete), and a WebRTC streaming screen with touch input. Android is the only supported client platform — iOS distribution was deliberately ruled out for a free/open-source project.
- **Windows is a first-class host now**: the server builds and runs on Windows with WHPX-accelerated emulators and AMD `h264_amf` hardware encoding (see [Windows host](#windows-host)).
- **Legacy**: `apps/controlplane` (TypeScript) and `apps/admin` are the previous implementation/reference and are not part of the running Go system. `apps/admin` is not served by the Go control plane yet.

## Architecture

```
apps/server/
  cmd/mako-controlplane   # auth, instances, sessions, admin/client APIs
  cmd/mako-worker         # one process per running instance
  internal/controlplane/  # auth, instance, session, transport, admin, image,
                          #   placement, workerproc, reconcile, tlsidentity
  internal/worker/        # emulator runtime, WebRTC transport, gRPC capture
apps/client/              # Flutter Android client
```

- The control plane spawns one `mako-worker` process per running instance, health-checks it, and respawns/adopts the emulator if it dies.
- Every instance gets its own registered AVD cloned from a template; deleting the instance removes that AVD.
- `mako-worker` captures the emulator through its gRPC screenshot stream, encodes H.264 with a hardware encoder when available, and sends video/input over WebRTC directly to the client (media never goes through the control plane).
- A user may own multiple instances; each instance can be started, stopped, and connected independently. An owner can invite guests to one session with a short PIN.

## Quick start (Windows)

### 1. Prerequisites

- Go **1.27+**
- Android SDK with `emulator`, `platform-tools`, and at least one x86_64 system image
- An AVD created with Android Studio or `avdmanager` (for example `Medium_Phone_API_36.1`)
- `ffmpeg` with an H.264 encoder; on AMD GPUs use a build with `--enable-amf` and check `ffmpeg -encoders | findstr h264_amf`
- Windows Hypervisor Platform (WHPX) enabled — verify with `emulator -accel-check`
- For building the client only: Flutter 3.x, JDK 17, Android SDK

### 2. Environment

Set these before starting the control plane (the spawned worker inherits its environment):

| Variable | Example | Purpose |
|---|---|---|
| `MAKO_DB_PATH` | `E:\mako\mako.db` | SQLite database |
| `MAKO_WORKER_BINARY_PATH` | `E:\mako\mako-worker.exe` | Worker binary the control plane spawns |
| `MAKO_ANDROID_SDK_ROOT` | `%LOCALAPPDATA%\Android\Sdk` | Android SDK |
| `MAKO_ANDROID_EMULATOR_PATH` | `%LOCALAPPDATA%\Android\Sdk\emulator\emulator.exe` | Emulator binary (optional if SDK root is set) |
| `MAKO_FFMPEG_PATH` | `C:\ffmpeg\bin\ffmpeg.exe` | ffmpeg binary (optional if on `PATH`) |
| `MAKO_INSTANCE_DATA_ROOT` | `E:\mako\instance-data` | Per-instance cloned AVD directories |
| `MAKO_BASE_IMAGE_ROOT` | `E:\mako\base-images` | Captured base images |
| `STUN_URL` | `none` | `none` disables STUN; use it on a LAN |
| `MAKO_CLIENT_ADDR` | `0.0.0.0:3000` | Bind the client API on the LAN for phones |
| `MAKO_TLS_CERT_PATH` / `MAKO_TLS_KEY_PATH` | defaults beside the DB | Self-signed cert/key |

### 3. Build and run

```powershell
cd apps/server
go build -o ..\..\.bin\mako-worker.exe ./cmd/mako-worker
go build -o ..\..\.bin\mako-controlplane.exe ./cmd/mako-controlplane
```

Run both in separate terminals (or let the control plane spawn workers):

```powershell
# terminal 1 — control plane
$env:MAKO_DB_PATH = "E:\mako\mako.db"
$env:MAKO_WORKER_BINARY_PATH = "E:\mako\mako-worker.exe"
$env:MAKO_ANDROID_SDK_ROOT = "$env:LOCALAPPDATA\Android\Sdk"
$env:MAKO_FFMPEG_PATH = "C:\ffmpeg\bin\ffmpeg.exe"
$env:STUN_URL = "none"
$env:MAKO_CLIENT_ADDR = "0.0.0.0:3000"
.\mako-controlplane.exe
```

On first run, read the bootstrap secret from `<db>.bootstrap-secret` and create the first admin (admin API is loopback-only on `127.0.0.1:3001`):

```powershell
$secret = (Get-Content .\mako.db.bootstrap-secret -Raw).Trim()
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:3001/api/v1/auth/setup `
  -ContentType application/json `
  -Body (@{ username="admin"; password="change-me"; deviceLabel="pc"; bootstrapSecret=$secret } | ConvertTo-Json)
```

### 4. Build and run the Android client

```powershell
cd apps/client
flutter pub get
flutter build apk --debug
adb install -r build\app\outputs\flutter-apk\app-debug.apk
```

The login screen takes a server URL. Use `https://10.0.2.2:3000` from an emulator client (the emulator's alias for the host) or `https://<host-lan-ip>:3000` from a real phone. The client currently trusts any TLS certificate (no PIN pairing yet), so only do this on a trusted network.

Log in, tap **+**, pick a template from the server's discovered AVDs, and create as many instances as you want. Each running instance has its own **Connect** button.

### Firewall

The first time `mako-worker.exe` or `mako-controlplane.exe` listens, Windows Defender Firewall may prompt. Allow private networks. The client API (TCP 3000) and WebRTC UDP ports must be reachable from the phone.

## Testing

```powershell
cd apps/server
go test ./...
```

```powershell
cd apps/client
flutter analyze
```

## Limitations (current)

- **No audio yet.** Only video + touch input are carried over WebRTC.
- **Touch is single-pointer and injected through `adb shell input`.** Fine for tapping/light use; multi-touch and low-latency drags via the emulator's gRPC `sendTouch` are the next transport work.
- **Trusted LAN only.** The client API uses a self-signed certificate and the client accepts any certificate; PIN pairing/SPKI pinning is still deferred. Do not expose it to the public internet without a VPN or reverse proxy.
- **No capacity enforcement.** Each instance costs a full emulator (RAM/CPU/disk); start only what the host can carry.
- Base-image capture/promote is unit-tested, but its live boot verification is still open (see [`docs/windows-migration-handoff.md`](docs/windows-migration-handoff.md) and the decisions log).

## Documentation

- [`docs/architecture/overview.md`](docs/architecture/overview.md) — current system shape and package map
- [`docs/architecture/data-model.md`](docs/architecture/data-model.md) — schema, including multi-instance ownership
- [`docs/architecture/api-surfaces.md`](docs/architecture/api-surfaces.md)
- [`docs/architecture/decisions-log.md`](docs/architecture/decisions-log.md)
- [`docs/plans/roadmap.md`](docs/plans/roadmap.md)
- [`docs/windows-migration-handoff.md`](docs/windows-migration-handoff.md) — Windows-specific findings and verification notes
