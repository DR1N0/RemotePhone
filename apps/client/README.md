# mako client (Android)

Flutter client for [mako](../..) — it logs in to a `mako-controlplane` server, lists the user's managed Android instances, and streams one over WebRTC with touch input.

Android is the only supported platform (iOS distribution was deliberately ruled out for a free/open-source project).

## Build

```powershell
flutter pub get
flutter build apk --debug
adb install -r build\app\outputs\flutter-apk\app-debug.apk
```

## Run on a connected device

```powershell
flutter run
```

## Server URL

- Android emulator client: `https://10.0.2.2:3000` (the emulator's alias for the host)
- Real phone: `https://<host-lan-ip>:3000`, with `MAKO_CLIENT_ADDR=0.0.0.0:3000` on the control plane and the Windows firewall allowing TCP 3000

The app currently trusts any TLS certificate because the server uses a self-signed certificate and PIN pairing/SPKI pinning is not implemented yet. Use it only on a trusted LAN.

## What works

- Login, registration with an invite code
- Template picker backed by the server's discovered AVDs
- Multiple instances per user: create, start, stop, delete, connect
- WebRTC video (hardware H.264 when available) and single-pointer touch

## Not implemented yet

- Audio
- Multi-touch / low-latency input via the emulator's gRPC input API
- PIN-based session invites in the UI (server endpoints exist)
- Certificate pairing/pinning
