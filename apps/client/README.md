# mako client (Android)

Flutter client for [mako](../..) — it logs in to a `mako-controlplane` server, lists the user's managed Android instances, and streams one over WebRTC with touch input.

Android is the only supported platform (iOS distribution was deliberately ruled out for a free/open-source project).

## Build

```powershell
flutter pub get
# Optional: bake in the server URL (otherwise the add-server screen defaults to 10.0.2.2:3000)
flutter build apk --debug --dart-define=MAKO_DEFAULT_SERVER=https://192.168.1.7:3000
adb install -r build\app\outputs\flutter-apk\app-debug.apk
```

## Run on a connected device

```powershell
flutter run
```

## Server URL

- Android emulator client: `https://10.0.2.2:3000` (the emulator's alias for the host)
- Real phone: `https://<host-lan-ip>:3000`, with `MAKO_CLIENT_ADDR=0.0.0.0:3000` on the control plane and the Windows firewall allowing TCP 3000

The app pins the server certificate after a successful access-code redemption or login. A known server is matched by stable server ID and certificate pin; the code field is skipped. Use it only on a trusted network for the first contact.

## What works

- Unified access-code login (self code or admin code), with account creation and certificate pinning in one step
- Multiple paired servers remembered on the phone, grouped by server with one row per account; a server can have several accounts
- One "Add server" flow: URL → access code only when no saved profile matches the server ID and certificate → username/password. Known servers skip the code; there is no separate Pair button or client-side registration.
- The certificate pin is stored in the server profile only; Forget server removes it, so re-adding requires a new access code. Two servers sharing an IP are distinguished by their server IDs and separate profiles.
- A server menu action creates a self code for the current account, or an admin code that can create a new user or pair an existing account
- mDNS Nearby discovery, plus manual URL entry
- Template picker backed by the server's discovered AVDs
- Multiple instances per user: create, start, stop, delete, connect
- WebRTC video (hardware H.264 when available) and audio (PCMU), with Android routing it as media (speaker + media volume, not the call earpiece)
- Multi-touch with per-finger pointer IDs, injected via the emulator's gRPC `sendTouch`
- Full-screen streaming: the phone's system bars are hidden, the guest uses three-button navigation inside the video, and the phone's Back gesture disconnects and returns to the instance list

## Not implemented yet

- Opus audio (currently 8 kHz mono PCMU)
- PIN-based session invites in the UI (server endpoints exist)
