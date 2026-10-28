# Changelog

## v0.0.1 — 2026-10-28

The first and final public snapshot of RemotePhone.

This is not a supported release. It is a paused prototype published so the
engineering does not disappear with the project.

### Included

- Go control plane with per-instance worker processes.
- Android emulator instance lifecycle, template discovery, AVD cloning, and
  QCOW2 base-image layering.
- WebRTC video/audio streaming, native multi-touch, keyboard input, and
  orientation-aware capture.
- Flutter Android client with access-code pairing, mDNS discovery, certificate
  pinning, instance management, and a collapsible guest Back/Home/Exit menu.
- Embedded React owner console.
- Tests across the control plane, worker, capture, transport, auth, and client
  analysis.

### Known limitations

- **Zenless Zone Zero / Genshin Impact / Honkai: Star Rail do not work.** The
  emulator's x86-to-ARM translation layer and the games' anti-cheat
  (`libtersafe2`) abort after login. This is the reason the project was paused.
- LAN-only, self-signed TLS; not hardened for public exposure.
- No installer, CI release pipeline, migration story, or GPU capacity control.
- Shader-cache base-image baking and account-wipe preparation were not finished.
- Android client only.
- Tested on one Windows host and one physical phone; no performance guarantees.

### Why this exists

RemotePhone was an attempt at self-hosted, multi-instance, touch-native Android
cloud gaming. Once the target games proved impossible to run on the emulator
runtime, the project no longer had a coherent product thesis. See the README
for the full post-mortem and the anti-cheat evidence.
