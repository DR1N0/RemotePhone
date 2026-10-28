# RemotePhone

**状态：无限期暂停 · v0.0.1 · 维护模式的公开快照**

[English](README.md) · [Release notes](docs/releases/v0.0.1.md) · [复用地图](docs/reuse-map.md) · [工程笔记](docs/engineering-notes.md) · [GitHub release](https://github.com/DR1N0/RemotePhone/releases/tag/v0.0.1)

RemotePhone 是一个自托管的 Android 模拟器串流实验。控制面、Flutter 客户端、触摸/横竖屏和 QCOW2 基础镜像都做出来了；但目标游戏过不了反作弊，所以项目停在这里。

仓库现在处于**维护模式**：不再加新功能，也不承诺支持；保留公开作为参考实现。想复用代码，先看[复用地图](docs/reuse-map.md)和[工程笔记](docs/engineering-notes.md)。贡献和安全边界见 [`CONTRIBUTING.md`](CONTRIBUTING.md) 和 [`SECURITY.md`](SECURITY.md)。

---

## 项目复盘

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

控制面、访问码/证书固定、QCOW2 基础镜像分层、WebRTC 触摸和方向处理都可以复用。入口、耦合和坑见[复用地图](docs/reuse-map.md)；最花时间的失败见[工程笔记](docs/engineering-notes.md)。

比较诚实的后续路线只有三条：

1. **真实 Android 手机**做 runtime：原生触摸、原生反作弊；
2. **Windows PC 原生游戏串流**：接受 Sunshine/Moonlight 已经占据这块市场；
3. **轻量、对模拟器友好的 Android 应用**继续用这套 emulator runtime。

我不建议继续在 Android x86 模拟器上对抗 AAA 手游反作弊。那是环境边界，不是这个仓库能修的 bug。

### 唯一还没试过的路线：Apple Silicon

绝区零没有官方 x86_64 APK，安卓包只有 `arm64-v8a`。但在 **Apple Silicon Mac** 上可以跑 **arm64 Android 系统镜像**，不需要把 ARM 代码翻译成 x86，因此有机会绕过 `libndk_translation` 这次崩溃。

Go 服务端确实能交叉编译到 `darwin/arm64`（已验证），但我没有在 macOS 上跑过完整链路，也没有验证 arm64 模拟器镜像、GPU/性能，以及反作弊在那种环境下是否买账。Intel Mac 会重复 x86 翻译问题，没有意义。这条路线只能算实验，不是受支持方案。

### 许可与商标

MIT，见 [`LICENSE`](LICENSE)。

RemotePhone 与 miHoYo/米哈游、HoYoverse、腾讯、Google、Android Open Source Project 均无隶属或背书关系。游戏名称和商标归各自权利人所有。
