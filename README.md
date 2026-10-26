# mako

Mako hosts Android emulator instances on your own machine and streams them to an Android phone or tablet over a trusted LAN. Each instance is a fully independent Android device: native touch input (not a virtual gamepad), its own cloned AVD/userdata, its own worker process, and its own WebRTC video stream.

It is built for the case desktop-streaming tools cannot express: playing touch-native mobile games on a phone, and letting several people drive several independent emulator "seats" on one host.

## Status

- **Server (Go)**: active implementation in [`apps/server`](apps/server). Auth, multi-tenant users/credentials, multi-instance-per-owner, template discovery, instance lifecycle, sessions/PIN invites, WebRTC transport, admin API, worker supervision, HTTPS on the client API, and base-image capture/promote are implemented and covered by tests.
- **Client (Flutter, Android)**: [`apps/client`](apps/client) has access-code login/pairing, mDNS server discovery, a template picker, a multi-instance list (start/stop/connect/delete), and a WebRTC streaming screen with touch input. Android is the only supported client platform — iOS distribution was deliberately ruled out for a free/open-source project.
- **Windows is a first-class host now**: the server builds and runs on Windows with WHPX-accelerated emulators and AMD `h264_amf` hardware encoding (see [Windows host](#windows-host)).
- **Legacy**: `apps/admin` is a previous browser UI/reference and is not served by the Go control plane. The TypeScript control plane (`apps/controlplane`) was removed on 2026-10-26.

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
| `MAKO_STREAM_BITRATE` | `2000000` | H.264 bitrate (bits/s); lower it for weak WiFi |
| `MAKO_AUDIO_DEBUG` | `1` | Log audio packet RMS/peak and `WriteSample` stalls (diagnostics only) |
| `MAKO_HEADLESS` | `1` | One worker owns one instance; default `-no-window`. Set `0` only to debug with a visible emulator window. |
| `MAKO_EMULATOR_AUDIO` | `none` | Emulator `-audio` backend. `none` keeps guest audio for `streamAudio` but plays nothing on the host; use `auto` for local debugging with host sound. |
| `MAKO_PAIRING_PIN` | random 6 digits | Local bootstrap access code printed at control-plane startup and seeded as a one-use code; set a stable value for automation. |
| `MAKO_SERVER_NAME` | host name | Display name shown in the client's server picker. The stable server ID is independent of it. |
| `MAKO_SERVER_ID_PATH` | `<db>.server-id` | Persisted UUID identifying this server across restarts/IP changes. |
| `MAKO_DISABLE_MDNS` | unset | Set `1` to disable LAN mDNS advertisement (`_mako._tcp`). |
| `MAKO_WEBRTC_HOST_IP` | unset | Optional debug override: restrict ICE to one IPv4 address. By default every routable IPv4 interface (LAN, Tailscale, other VPNs) is advertised and ICE chooses. |

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
$env:MAKO_STREAM_BITRATE = "2000000"
# Optional debug override only (normally leave unset so LAN + Tailscale are
# both advertised and ICE picks the reachable path):
# $env:MAKO_WEBRTC_HOST_IP = "192.168.1.7"
.\mako-controlplane.exe
```

On first run, read the bootstrap secret from `<db>.bootstrap-secret` and create the first admin (admin API is loopback-only on `127.0.0.1:3001`):

```powershell
$secret = (Get-Content .\mako.db.bootstrap-secret -Raw).Trim()
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:3001/api/v1/auth/setup `
  -ContentType application/json `
  -Body (@{ username="admin"; password="change-me"; deviceLabel="pc"; bootstrapSecret=$secret } | ConvertTo-Json)
```

After creating the first admin, pair the Android client with either the startup bootstrap access code or an owner-generated code:

```powershell
Set-Content -NoNewline $env:TEMP\code.json '{}'
curl.exe -H "Content-Type: application/json" `
  --data-binary "@$env:TEMP\code.json" `
  http://127.0.0.1:3001/api/v1/owner/access-codes
```

Use the returned code with the admin username/password in the client.

### 4. Build and run the Android client

```powershell
cd apps/client
flutter pub get
# Optional: bake in the server URL so a real phone does not need to type it
flutter build apk --debug --dart-define=MAKO_DEFAULT_SERVER=https://192.168.1.7:3000
adb install -r build\app\outputs\flutter-apk\app-debug.apk
```

The app opens on **Servers**, even with no saved account. Servers discovered by mDNS appear under **Nearby**; **+** opens manual entry. The add-server flow is URL → access code (only when the certificate is not already trusted) → username/password. Access codes are unified:

- A **self code** is created by any logged-in user for their own account. Use it on a new phone to add that device.
- An **admin code** is created by an `admin` user. It is not bound to an account: the server decides at redemption whether to create a new ordinary user or add a device to an existing account, based on whether the username already exists. It never creates an admin.
- The certificate pin is stored only in the saved server profile, keyed by the server's stable UUID. Adding/known-server detection always fetches `/server/info`, finds profiles for that server ID, and trusts the connection only if the certificate observed on that connection equals the profile's pinned hash. The same IP can therefore host two different servers: each server ID selects its own profile and pin. "Forget server" removes the profile and its pin, so re-adding requires a fresh access code. Clearing app data does the same.

Create an admin code on the client API:

```powershell
Set-Content -NoNewline $env:TEMP\code.json '{}'
curl.exe -k -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" `
  --data-binary "@$env:TEMP\code.json" `
  https://192.168.1.7:3000/api/v1/access-codes
```

An ordinary user's self code uses the same endpoint. The server returns `scope: "self"` or `scope: "new-or-existing"` and an expiry. Redeem with `POST /api/v1/access-codes/redeem` (`code`, `username`, `password`); the response includes `created`, the token, and `certSha256` for pinning.

The loopback owner console on `127.0.0.1:3001` has no bearer-token requirement because reaching it already requires host access; it is protected against browser CSRF/DNS rebinding. It can create a code for any target (or unbound) and reset a password:

```powershell
Set-Content -NoNewline $env:TEMP\code.json '{}'
curl.exe -H "Content-Type: application/json" `
  --data-binary "@$env:TEMP\code.json" `
  http://127.0.0.1:3001/api/v1/owner/access-codes
```

If the only admin loses both the certificate pin and the password, recovery happens on the server: reset the password at `POST /api/v1/owner/users/reset-password` and mint an owner access code. This is the unavoidable out-of-band trust recovery path.

After redemption, the URL and server ID both retain the certificate pin; later logins verify the pin and hide the code field.

### Firewall

The first time `mako-worker.exe`, `emulator.exe`, or either qemu binary listens, Windows Defender Firewall may prompt. Allow private networks. Running test tools with `go run` creates a new temporary executable each time and therefore re-prompts every run — build them to a fixed path instead.

For a one-time setup, run [scripts/windows/firewall-allow.ps1](scripts/windows/firewall-allow.ps1) from an elevated PowerShell. It adds inbound allow-rules for the fixed binaries (including the headless qemu and `.bin\webrtc.test.exe`) and, with `-DisableNotifications`, also silences the "blocked app" popup for unknown programs. After that, starting `mako-controlplane` is enough: it spawns each instance's worker and emulator itself.

`go test` normally builds a new temporary test executable, which cannot be covered by a fixed-path firewall rule. For the WebRTC transport tests, build once to the fixed path:

```powershell
cd apps/server
go test -c -o ..\..\.bin\webrtc.test.exe ./internal/worker/transport/webrtc
..\..\.bin\webrtc.test.exe -test.v
```

The client API (TCP 3000) and WebRTC UDP ports must be reachable from the phone.

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

- **Audio is 8 kHz mono μ-law (PCMU), not Opus.** It is carried over WebRTC and playable on the Android client, but it is telephone-quality; Opus is the next quality step.
- **Multi-touch is implemented through the emulator's gRPC `sendTouch`** (pointer IDs are carried from the client), with an adb single-touch fallback if gRPC input fails. Keyboard events still use adb.
- **Full-screen phone UI; the guest owns navigation.** The client hides the phone's system bars, the worker switches the emulated device to three-button navigation, and the guest's Back/Home/Recents buttons are tapped inside the video. The phone's Back gesture exits the stream and disconnects.
- **Trusted LAN only.** The server uses a self-signed certificate; first contact is bootstrapped with an access code and the client pins the certificate afterwards. Do not expose it to the public internet without a VPN or reverse proxy.
- **ICE advertises every routable IPv4 interface** (LAN, Tailscale, other VPNs), excluding only link-local, multicast and IPv6; loopback is kept for local probes. ICE chooses. `MAKO_WEBRTC_HOST_IP` is only a debug override. There is no Tailscale-specific configuration.
- **An Android emulator is a poor test client on the same Windows host.** Its NAT hairpin to the host's own LAN IP loses packets under video load; a real phone on the LAN is the trustworthy end-to-end test. The host-side WebRTC probe is unaffected.
- **No capacity enforcement.** Each instance costs a full emulator (RAM/CPU/disk); start only what the host can carry.
- Base-image capture/promote is unit-tested, but its live boot verification is still open (see [`docs/windows-migration-handoff.md`](docs/windows-migration-handoff.md) and the decisions log).

## Documentation

- [`docs/architecture/overview.md`](docs/architecture/overview.md) — current system shape and package map
- [`docs/architecture/data-model.md`](docs/architecture/data-model.md) — schema, including multi-instance ownership
- [`docs/architecture/api-surfaces.md`](docs/architecture/api-surfaces.md)
- [`docs/architecture/decisions-log.md`](docs/architecture/decisions-log.md)
- [`docs/plans/roadmap.md`](docs/plans/roadmap.md)
- [`docs/windows-migration-handoff.md`](docs/windows-migration-handoff.md) — Windows-specific findings and verification notes
