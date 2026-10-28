# Engineering notes

These are the failures that cost the most time. They are more useful than a
feature list if you want to learn from the project or borrow a piece of it.

The short version: **the emulator is not a stable display server, the translation
layer is not transparent, and a green unit test does not prove a spawned process
works.**

## 1. Emulator frames can be rotated relative to the AVD

The first real-client stream connected but showed black. The worker was dropping
every frame because it expected the AVD's 540x960 portrait size, while the
emulator's gRPC screenshot stream returned a 432x960 portrait buffer whose
composition was already rotated 90 degrees. The game had forced landscape, but
the AVD metadata had not changed.

The fix is in `internal/worker/transport/webrtc/adapter.go` and `encoder.go`:

- wait for the first frame, then query the emulator's current display rotation;
- choose the encoder dimensions from the actual frame, not the AVD metadata;
- when the frame is still portrait but the display is rotated, apply an ffmpeg
  transpose filter and swap the encoded dimensions;
- map input coordinates back through the same rotation before calling
  `sendTouch` (for `ROTATION_90`, the inverse is `(H-y, x)`).

A mid-session rotation change now restarts ffmpeg with the new geometry. Before
that, reconnecting was the only way to recover.

**Lesson:** treat the screenshot stream as the source of truth for geometry, not
the AVD config.

## 2. Multi-slice H.264 inflates RTP timestamps

`pipeH264` originally treated every NAL unit as a complete access unit. `libx264
-tune zerolatency` emits one slice per CPU core (16 on the test host), so one
picture became 16 RTP samples and the timestamp advanced roughly 16x too fast.
The client reported hundreds of "fps" while the actual stream was ~30 fps.

The fix groups NALs into access units using `first_mb_in_slice == 0` (plus AUD
NALs when present). Continuation slices stay in the same sample. The regression
test is in `internal/worker/transport/webrtc/pipeh264_test.go`.

**Lesson:** if the client's frame counter looks impossible, inspect the RTP
timestamp deltas before blaming the decoder.

## 3. Do not recreate the Flutter video renderer per session

Reconnect produced decoded-but-black video on alternating sessions. Worker logs
showed fresh frames and audio, and a direct gRPC capture proved the emulator
content was not black. The Flutter client was disposing its `RTCVideoRenderer` on
every stream exit and creating a new texture on the next screen. On Android that
intermittently left the new texture black even while `framesDecoded` advanced.

The client now keeps one app-lifetime renderer (`_sharedRenderer` in
`apps/client/lib/services/webrtc_session.dart`) and only swaps its `srcObject`.

**Lesson:** a texture that is "decoding but black" can be a renderer lifecycle
bug, not a codec or network bug.

## 4. `exec.Cmd.Env` replaces the child environment

The real worker spawn failed on the first end-to-end run even though every unit
test passed. `workerproc.Spawn` built the child environment with
`append(extraEnv, "REMOTEPHONE_WORKER_...")`. With `extraEnv == nil`, the spawned
worker received only those three variables: no `PATH`, no `HOME`, no
`REMOTEPHONE_ANDROID_SDK_ROOT`. It found zero templates, and `CreateManagedInstance`
failed with "runtime template not found".

The fix passes `os.Environ()` as the base environment and appends the worker
overrides. No fake `Execer` test could catch this, because the fakes answer HTTP
directly and never consult `PATH` or the SDK root.

**Lesson:** a fake-`Execer` test proves the call sequence, not that the spawned
process can do its job. Run one real spawn before trusting the orchestration.

## 5. QCOW2 capture is not a file copy

The base-image idea is sound: after first boot, the emulator stops writing guest
state directly into `userdata-qemu.img` and writes into
`userdata-qemu.img.qcow2`. A capture has to merge that overlay into the backing
file with `qemu-img commit` before copying `userdata-qemu.img` out, and the
encryption key has to travel with it.

What was not finished is the live path. New instances seeded from a captured and
promoted base image hung during boot in three out of three attempts. A minimal
manual reproduction worked, which proved the merge mechanism in principle, but
the product path did not. The leading theory was an unclean guest shutdown
leaving a filesystem that needed a long recovery pass; a later control test also
showed the host itself had degraded after a very long development session, so the
result is not fully attributed.

Before trusting this feature, use a graceful guest shutdown, reproduce on a
freshly rebooted host, and bisect fresh versus heavily used instances. See the
2026-10-20 entry in `docs/architecture/decisions-log.md`.

**Lesson:** storage-layer features need a clean-host, clean-shutdown test matrix.
"This worked once by hand" is not verification.

## 6. x86-to-ARM translation plus anti-cheat is an environment boundary

Zenless Zone Zero ships `arm64-v8a` libraries only. The Android Studio x86_64
emulator runs them through `libndk_translation` / berberis. After login, the game
loads `libtersafe2.so` (Tencent anti-cheat), and the process aborts:

```text
Fatal signal 6 (SIGABRT)
Abort message: 'Guest call didn't restore sp: expected ..., actual ... -0x10'
#04 libndk_translation.so (berberis::ExecuteGuestCall)
```

Clearing the Vulkan PSO cache did not help; it only forced a ~261 MB re-download.
Changing encoders or restarting the game left the same native abort.

This is not a rendering bug in RemotePhone. It is the anti-cheat interacting with
the translation boundary. Treat it as an environment constraint, not a patch
target. An Apple Silicon arm64 emulator image is the only untested escape hatch;
the Go server cross-compiles for `darwin/arm64`, but the full stack, GPU path,
performance, and anti-cheat behavior were never verified.

**Lesson:** if the target app has kernel-level or native anti-cheat, validate the
runtime before building the product around it.

## 7. Windows GPU mode is not a capacity plan

`REMOTEPHONE_GPU_MODE=host` matters: without it, the emulator can silently fall
back to SwiftShader, which is fine for a launcher and unusable for a 3D game. On
the test host, an explicit host-GPU run selected the AMD RX 9070 through
Vulkan/GLES with WHPX enabled, even headless. H.264 encoding used `h264_amf` at
the real capture size.

But every instance shares the same GPU, VRAM, and hardware encoder. There is no
per-instance GPU isolation or capacity enforcement. Multi-instance capacity is
not a disk-only question.

**Lesson:** check the emulator's actual renderer, not just the AVD setting. A
"host GPU" flag can still end up on a software path.

## 8. macOS cross-compilation is not macOS support

The Go binaries build for `darwin/arm64`. That is the extent of the verification.
The macOS emulator launch path, GPU path, encoder selection, and the arm64 system
image were never run end to end. Apple Silicon also does not provide a usable
32-bit ARM execution environment, so classic `armeabi-v7a` games are a poor fit
for that host; use a Windows x86_64 emulator with a translation layer if that is
the goal.

**Lesson:** a cross-compile proves the compiler can emit a binary, not that the
runtime exists.

## 9. The security model is a trusted LAN, not a public service

The design is self-signed TLS, one-time access codes, certificate pinning, and a
loopback-only owner console. It was never hardened for public exposure. There is
no multi-tenant isolation, no resource quota, no DDoS protection, and not every
redemption path has brute-force rate limiting. APKs and emulator images should be
treated as untrusted code.

See `SECURITY.md` before deploying anything.

## 10. What I would do differently

- Validate the runtime against the actual target app before building orchestration.
- Start with a real Android device if anti-cheat is in scope; native touch and
  native execution avoid the entire translation problem.
- Decide whether the product is a device farm, a remote Android runtime, or a
  game-streaming app before writing the control plane. They need different APIs.
- Build one native client well instead of a browser client and a native client.
- Add a public automation API earlier; the control plane is most useful when
  something other than the Flutter app can drive it.
- Run one real end-to-end test per phase. Fakes are for speed, not for truth.
