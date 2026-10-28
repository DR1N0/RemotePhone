# RemotePhone

**Status: paused indefinitely · v0.0.1 · a finished experiment, not a product**

[Release notes](docs/releases/v0.0.1.md) · [GitHub release](https://github.com/DR1N0/RemotePhone/releases/tag/v0.0.1) · [中文](#中文)

RemotePhone started as a simple idea: run several Android emulators on my own PC, stream each one to a phone with real touch input, and let a few people each have their own "phone" on the same machine.

It got surprisingly far. Then it ran into miHoYo's anti-cheat and stopped there.

This repository is the honest snapshot of that attempt: the control plane works, the client works, the streaming/touch/orientation plumbing works — but the one use case that made the project interesting, playing games like **Zenless Zone Zero** and **Genshin Impact** through an Android emulator, does not work, and I do not have a responsible way around it.

---

## English

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

### License and trademarks

MIT. See [`LICENSE`](LICENSE).

RemotePhone is not affiliated with or endorsed by miHoYo/HoYoverse, Tencent, Google, or the Android Open Source Project. Game names and trademarks belong to their respective owners.

---

## 中文

### 这个项目原本想做什么

一开始的想法很朴素：在**自己的 Windows 电脑**上开几台 Android 模拟器，把画面和触摸直接串到手机上。每个用户拿到一台独立的模拟器，而不是一个套着虚拟手柄的 PC 桌面串流；每台实例有自己的 AVD、userdata、worker 和 WebRTC 流；游戏资源用 QCOW2 基础镜像 + 薄层复制，避免每个用户都复制 30GB；手机端发送的是真正的触摸/按键输入。

这才是我觉得值得做的地方。单纯做一个 PC 串流器没有意义——Sunshine/Moonlight 已经很成熟，我没有必要重复造一遍。

### 为什么放弃

**绝区零的反作弊，在模拟器的 ARM 翻译层里直接崩了。**

具体来说：

- 绝区零只带 `arm64-v8a` 原生库；
- Android Studio 的 x86_64 模拟器要用 `libndk_translation` / berberis 翻译执行；
- 登录后游戏加载 `libtersafe2.so`（腾讯反作弊），进程随即 abort：

```text
Fatal signal 6 (SIGABRT)
Abort message: 'Guest call didn't restore sp: expected ..., actual ... -0x10'
#04 libndk_translation.so (berberis::ExecuteGuestCall)
```

这个问题可以稳定复现。清 Vulkan PSO cache 没有用，只会让游戏重新下载约 261MB；换编码器、重启游戏也一样。它不是 RemotePhone 的渲染 bug，而是反作弊和 x86→ARM 翻译边界撞在了一起。

原神/星穹铁道这类体量的米哈游游戏也属于同一类风险。早年的“PC 客户端切手机/触屏模式”启动参数是未公开玩法，后来失效了；我扫了一遍本机绝区零 PC 安装目录，也没有发现可切换的移动端 UI/触屏模式。用 PC 客户端 + 虚拟手柄是另一条产品路线，而那条路线已经有 Sunshine/Moonlight。

于是项目失去了它最核心的东西：**原生触摸 + 独立 Android 实例**。一个跑不了目标游戏的 LAN 串流服务，工程上再有趣，也不是产品。

### 目前还能用的部分

- Go 控制面 + 每实例 worker；
- 账号、统一访问码、证书固定、仅本机 owner console；
- mDNS 服务发现；
- 模拟器实例的创建/启动/停止/删除/接管；
- QCOW2 基础镜像分层（包括 encryption key 的传递）；
- WebRTC H.264 视频和 PCMU 音频；
- 原生多点触摸和按键输入；
- 横竖屏采集归一化和触摸坐标映射；
- 右侧可折叠浮层（受控机 Back/Home + 退出串流）；
- Flutter Android 客户端和嵌入式 React owner console；
- 控制面、worker、采集/传输、鉴权的测试。

它仍然可以拿来跑**轻量、对模拟器友好的 Android 应用**。但它从来没有为生产环境、多租户并发或不可信网络做过加固。

### 哪些不行 / 没做完

- **绝区零、原神、星穹铁道**：反作弊 + ARM 翻译，当前路线无法登录。
- **生产运维**：没有安装器、没有 CI 发布流程、没有升级/迁移方案、没有 GPU 容量控制。
- **基础镜像收尾**：登录后编译 shader 再打进 base、抹除登录信息，没有实现；镜像 GC、异步采集进度也还空着。
- **安全加固**：只适合可信 LAN；自签名证书；不要直接暴露到公网。
- **跨平台客户端**：只有 Android，没有 iOS/桌面客户端。
- **性能承诺**：只在一台 Windows 主机和一台手机上验证过，不是 benchmark。

### 如果还想跑起来

前置条件：Go 1.27+、Android SDK/emulator 和 x86_64 镜像、带 H.264 的 ffmpeg、Windows 上启用 WHPX，以及构建客户端用的 Flutter/JDK 17。

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

然后打开 `http://127.0.0.1:3001/admin/`，构建并安装客户端：

```powershell
cd apps/client
flutter pub get
flutter build apk --debug --dart-define=REMOTEPHONE_DEFAULT_SERVER=https://192.168.1.10:3000
adb install -r build\app\outputs\flutter-apk\app-debug.apk
```

Android 包名是 `com.remotephone.client`。客户端通过 `_remotephone._tcp` mDNS 发现服务，也可以手动填 URL。首次连接需要一次性访问码，之后会固定服务器证书。

完整的环境变量、防火墙和 API 细节在 [`docs/`](docs/) 里，保留原样作为归档文档；README 不再假装它是活跃产品。

### 如果以后有人接手

控制面、访问码/证书固定、QCOW2 基础镜像分层、WebRTC 触摸和方向处理都可以复用。

比较诚实的后续路线只有三条：

1. **真实 Android 手机**做 runtime：原生触摸、原生反作弊；
2. **Windows PC 原生游戏串流**：接受 Sunshine/Moonlight 已经占据这块市场；
3. **轻量、对模拟器友好的 Android 应用**继续用这套 emulator runtime。

我不建议继续在 Android x86 模拟器上对抗 AAA 手游反作弊。那是环境边界，不是这个仓库能修的 bug。

### 许可与商标

MIT，见 [`LICENSE`](LICENSE)。

RemotePhone 与 miHoYo/米哈游、HoYoverse、腾讯、Google、Android Open Source Project 均无隶属或背书关系。游戏名称和商标归各自权利人所有。
