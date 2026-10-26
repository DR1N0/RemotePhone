# Local Transport End-to-End Acceptance Runbook

> **Historical.** This runbook predates the Go port and the removal of the legacy TypeScript control plane (`apps/controlplane`, removed 2026-10-26). Its commands are no longer runnable; current verification is `go test ./...` in `apps/server` plus the Android client build. The design notes remain for context.

This runbook records local Worker/transport acceptance and the supported browser development diagnostic. It is intentionally limited to macOS, Android Studio Emulator, and Google Chrome on the server host. The ordinary native client is a separate project; this admin WebUI is not its browser replacement.

## Prerequisites

- macOS with Android Studio installed and an installed AVD. Discover names with:

  ```bash
  "$HOME/Library/Android/sdk/emulator/emulator" -list-avds
  ```

- Android SDK platform tools, including `adb`:

  ```bash
  adb start-server
  adb devices -l
  ```

  Before starting the flow, the chosen emulator must appear in the output with state `device`.

- Google Chrome on the Control Plane host for the optional development diagnostic.
- Go 1.24 or later (the Worker module declares `go 1.24`).
- Node.js and npm compatible with the checked-in lockfiles. The execution environment must record the exact versions below.
- A controlled network: the Control Plane's client and management listeners remain loopback-only. Public NAT traversal/TURN is not part of this flow.
- Available local ports: Worker `127.0.0.1:7780`, client API `127.0.0.1:3000`, management WebUI/API `127.0.0.1:3001`, and (optionally) Chrome remote debugging `9222`.

### Environment captured for this run

| Item | Required | Observed before execution |
| --- | --- | --- |
| Host OS | macOS | macOS 26.5.2 (Darwin 25.5.0 arm64) |
| Google Chrome | installed | Google Chrome 152.0.7977.134 extended |
| Android Studio | installed | `/Applications/Android Studio.app` |
| AVDs | at least one installed | `Medium_Phone_API_36.1`, `Medium_Tablet` |
| `adb` | installed | Android Debug Bridge 1.0.41 / 36.0.2-14143358 at `/opt/homebrew/bin/adb` |
| Go | 1.24 or later | go1.26.5 darwin/arm64 |
| Node.js | record exact version | v24.9.0 |
| npm | record exact version | 11.6.0 |

## Build and service commands

Run these commands from the repository root. `npm ci` is required; do not replace it with `npm install`.

```bash
# Build the Admin before launching the Control Plane. The diagnostic is absent unless
# this build-time flag is true.
cd apps/admin && npm ci && VITE_MAKO_ENABLE_SESSION_DIAGNOSTIC=true npm run build

# Terminal 1: local Worker. The explicit SDK settings make Emulator discovery repeatable.
cd apps/worker
MAKO_ANDROID_SDK_ROOT="$HOME/Library/Android/sdk" \
MAKO_ANDROID_EMULATOR_PATH="$HOME/Library/Android/sdk/emulator/emulator" \
MAKO_WORKER_LISTEN_ADDR="127.0.0.1:7780" \
MAKO_WORKER_BASE_URL="http://127.0.0.1:7780" \
go run ./cmd/mako-worker

# Terminal 2: Control Plane. The backend diagnostic route also requires an explicit flag.
cd apps/controlplane
MAKO_CONTROLPLANE_PORT="3000" \
MAKO_ADMIN_PORT="3001" \
MAKO_ENABLE_SESSION_DIAGNOSTIC="true" \
MAKO_WORKER_BASE_URL="http://127.0.0.1:7780" \
npm run dev

# Terminal 3: verify the Worker and local management app, then open the WebUI.
curl -fsS http://127.0.0.1:7780/healthz
curl -fsS http://127.0.0.1:3001/healthz
open -na "Google Chrome" --args \
  --user-data-dir=/tmp/mako-chrome-diagnostic \
  --remote-debugging-port=9222 \
  http://127.0.0.1:3001/admin
```

`http://127.0.0.1:3000/admin` is intentionally a JSON `404`, not a redirect. Do not point Chrome at client port `3000` for setup, status, administration, documentation, or the WebUI.

## Development diagnostic procedure

The browser Session page is an explicitly enabled **development diagnostic** on the local management app only. It is not a general browser/native client, does not create Sessions or redeem invites, and does not grant media access to an administrator credential.

1. Use the separate native client/client API flow to create or redeem a Session and obtain the exact attachment ID plus its Attachment bearer credential. Keep this credential out of URLs, query strings, and logs.
2. On the server host, open `http://127.0.0.1:3001/admin/session/<session-id>` in Chrome.
3. Paste the attachment ID and its Attachment bearer credential into the diagnostic form. The page stores them in `sessionStorage` for that session ID and sends them as the normal `Authorization: Bearer` header for Session reads, SDP/ICE, and disconnect.
4. Verify the diagnostic only with the matching Attachment. An account/admin bearer is rejected and is never a substitute.

The former `/session/:id` invite redirect and query-string Attachment authority no longer exist. Do not use the retired owner/observer Chrome invite procedure below as current instructions; use the diagnostic procedure above for browser-only development checks, and use the native client for ordinary client/guest flows.

## Historical execution notes (pre-dual-surface)

The evidence below predates the `3000` client / `3001` management split. It is retained as historical transport evidence only. Any old `localhost:3000/admin`, browser invite redirect, or query-string credential reference is obsolete; follow the development diagnostic procedure above.

- **2026-09-20 13:20:15 CST — AVD readiness: PASS.** Command: `"$HOME/Library/Android/sdk/emulator/emulator" -avd Medium_Phone_API_36.1 -no-snapshot -no-boot-anim`, followed by `adb devices -l`, `adb -s emulator-5554 shell getprop sys.boot_completed`, and `adb -s emulator-5554 shell dumpsys window`. Actual result: `emulator-5554` reported `device`, `sys.boot_completed=1`, and the focused activity was `com.google.android.apps.nexuslauncher/.NexusLauncherActivity` (Android home screen).
- **2026-09-20 13:20–13:21 CST — Services and headed Chrome Admin: PASS.** The Worker `/healthz` returned `{"status":"ok","workerId":"local","hostOs":"macos","arch":"arm64","installedRuntimes":[{"runtimeClass":"android-studio-emulator","displayName":"Android Studio Emulator"}]}`. The Control Plane `/healthz` returned `{"status":"ok"}` and `GET /admin` returned `HTTP/1.1 200 OK` with `content-type: text/html; charset=utf-8`. A headed Google Chrome 152 owner profile opened `/admin`; its rendered body contained `mako admin`, `Create Instance`, and the instance table.
- **2026-09-20 13:23:10–13:24:40 CST — Worker-managed AVD: PASS after an initial environmental conflict.** Creating the dedicated `task6-transport-20260920` instance then starting it while the manually started copy of the same AVD was running failed with `FATAL | Running multiple emulators with the same AVD is an experimental feature.Please use -read-only flag to enable this feature.` The manual AVD was stopped with `adb -s emulator-5554 emu kill`; restarting the dedicated instance then reached Control Plane `observedState:"running"`, `adb` serial `emulator-5600` in state `device`, `sys.boot_completed=1`, and the Android launcher focused.
- **2026-09-20 13:25:xx CST — Transport ADB serial blocker: FAIL.** The instance assigned `adbPort:5601`, but the Worker transport adapter derives the serial `localhost:5601`. The exact probe `adb -s localhost:5601 get-state` returned `error: device 'localhost:5601' not found`, while the running emulator was available as `emulator-5600`. This prevents the required Worker-side touch forwarding check from succeeding in this environment.
- **2026-09-20 13:21–13:26 CST — Chrome/session automation blocker: BLOCKED.** The initial headed Chrome Admin tab was available through remote debugging, but the remote-debugging endpoint subsequently failed with `curl: (7) Failed to connect to 127.0.0.1 port 9222 after 0 ms: Couldn't connect to server`; the Chrome main process had restarted with `--restart`. A direct session creation attempt returned HTTP 500 to the initial `curl -fsS` call; its immediate replay returned `HTTP/1.1 409 Conflict` with `{"error":"active session already exists for instance 9eff0787-290d-4c2a-a89c-76b3762c5e36"}`. The documented API exposes no session-list route, so the active attachment could not be recovered through supported public routes to complete a real owner/observer Chrome sequence. No result below is marked successful on the basis of this partial setup.
- **2026-09-20 13:26:53 CST — Instance stop runtime cleanup: PARTIAL.** `POST /api/v1/instances/9eff0787-290d-4c2a-a89c-76b3762c5e36/stop` returned `desiredState:"stopped"`, `observedState:"stopped"`, and `runtimeHandle:null`. A subsequent `adb devices -l` was empty, and no `screenrecord`/Mako capture process was present. Because no browser attachment transport was successfully established, this does not verify removal of active adapters.
- **2026-09-20 13:18–13:20 CST — Required automated verification: PASS.** `cd apps/worker && go test ./...` passed **43 tests in 10 packages**. `cd apps/controlplane && npm ci && npm test -- --run && npm run build` passed **15 files / 76 tests** and built successfully; `npm ci` reached the configured registry without an authentication failure and reported **13 vulnerabilities (8 moderate, 4 high, 1 critical)**. `cd apps/admin && npm ci && npm test && npm run build` passed **1 file / 3 tests** and built successfully; `npm ci` reached the configured registry without an authentication failure and reported **9 vulnerabilities (6 moderate, 2 high, 1 critical)**. `make build` passed. Both Admin production builds emitted the existing Vite warning that the minified JavaScript chunk is **1,010.37 kB**, above the 500 kB warning threshold; this did not change their exit status.
- **2026-09-20 13:27 CST — Final required checks: PASS.** `cd apps/worker && go test ./... && cd ../controlplane && npm test -- --run && cd ../admin && npm test && npm run build` passed: Worker **43 tests in 10 packages**, Control Plane **15 files / 76 tests**, and Admin **1 file / 3 tests** plus a successful build. The same existing Admin large-chunk warning and React Router future-flag warnings were emitted without failing the command.

## Initial acceptance results (before root-cause fixes)

These historical results were superseded by the retry below. They remain recorded with their original evidence. `Not run` is not a pass.

| Check | Date | Command | Expected result | Actual result | Status |
| --- | --- | --- | --- | --- | --- |
| Owner video and input | 2026-09-20 | Headed Chrome owner `/admin` attempt; `POST /api/v1/sessions` for instance `9eff0787-290d-4c2a-a89c-76b3762c5e36`; `adb -s localhost:5601 get-state` | Android video arrives; owner input visibly affects the emulator. | The Admin dashboard rendered in real Chrome, but the Chrome debug endpoint restarted before its session UI could be driven. The first session-create command returned 500, its replay returned 409 active-session conflict, and `adb -s localhost:5601 get-state` returned `error: device 'localhost:5601' not found`. No video or forwarded owner input was observed. | Fail — blocked |
| Observer video and no input | 2026-09-20 | Observer Chrome invite flow and input attempt from step 5 | Observer receives video; Worker rejects input; Android is unchanged. | Blocked before an invite URL could be obtained from a recoverable active owner session. | Fail — blocked |
| Promote | 2026-09-20 | Owner `Promote` action, then observer input from step 6 | Observer becomes participant and its input visibly affects Android. | Blocked because no observer attachment could be created; Worker ADB serial probe also failed. | Fail — blocked |
| Demote | 2026-09-20 | Owner `Demote` action, then observer input from step 6 | Observer returns to observer role; Worker rejects its input; Android is unchanged. | Blocked because no observer attachment could be created; Worker ADB serial probe also failed. | Fail — blocked |
| Tab close cleanup | 2026-09-20 | Close observer Chrome tab/window, inspect Worker/capture or reconnect from step 7 | Attachment adapter/capture stops, or reconnect safely replaces it. | Blocked because no observer transport was established. | Fail — blocked |
| Reconnect | 2026-09-20 | Reopen the observer invite after closure from step 7 | A fresh safe transport supplies video without competing capture. | Blocked because no observer invite/transport was established. | Fail — blocked |
| Instance stop cleanup | 2026-09-20 | `POST /api/v1/instances/9eff0787-290d-4c2a-a89c-76b3762c5e36/stop`; `adb devices -l`; `pgrep -alf 'screenrecord|mako-worker.*capture'` | Instance adapters are removed and no Mako screen capture process remains. | Control Plane reported `stopped` with `runtimeHandle:null`; subsequent `adb devices -l` was empty and no capture process remained. No active adapter existed to prove adapter removal. | Fail — partial |

## Retry after ADB-serial and atomic-session fixes

**Date:** 2026-09-20. This retry used Worker fix `5dd3f02` and Control Plane fix `efd1606` on `main`.

### Retry execution notes

- The explicit Worker command from this runbook started successfully and reported the Android Studio Emulator runtime. The initial Control Plane launch against the existing repository database terminated during reconciliation with `Error: Worker request failed with status 404` from `LocalWorkerClient.inspectInstance`; that database contained an old persisted runtime handle not present in the new Worker. The retry used an isolated local database at `/tmp/mako-task6-20260920.db`, after which `GET /healthz` returned `{"status":"ok"}`.
- A new `task6-retry-20260920` instance was created and started through the headed owner Chrome Admin UI. Its runtime handle assigned console port `5600` and ADB port `5601`. `adb devices -l` reported `emulator-5600 ... device`, `adb -s emulator-5600 get-state` returned `device`, `sys.boot_completed=1`, and Android Launcher was focused. This verifies the fixed Worker serial convention (`emulator-<consolePort>`) reaches the AVD.
- Clicking `Launch Session` in owner Chrome made `POST /api/v1/sessions` appear in that page's browser performance entries with `responseStatus:201`. The rendered session showed `owner` and `you`, confirming an owner attachment was returned rather than the former 500/409 sequence.
- Owner and observer signaling both returned HTTP 200 and their headed Chrome pages displayed `live`; the Worker created one `adb ... screenrecord --output-format=h264` process per connected attachment. However, after waiting twelve seconds, both Chrome video elements remained `readyState:0`, `videoWidth:0`, `videoHeight:0`, `paused:true`, and had no `srcObject` tracks. No Android video was observed in either Chrome profile.
- The observer was created through the supported local `POST /api/v1/sessions/:id/join` route because the browser clipboard contents from `Copy Invite` could not be materialized into the command transcript. The attachment was connected as `observer`; this is a controlled local observer-attachment substitute, not successful invite-redemption coverage.
- In owner Chrome, `Promote` changed the observer Chrome role from `observer` to `participant`; `Demote` changed the same attachment back to `observer`. Browser role polling and the owner controls reflected both transitions. A synthetic participant click and a subsequent observer click left Android Launcher unchanged. Because Chrome did not render the Android video and the Worker intentionally discards input-handler errors, this does not prove successful participant input or Worker-side rejection of observer input.
- Closing the observer Chrome tab changed its attachment status to `disconnected` and reduced active `adb ... screenrecord` processes from two to one. Resuming that same attachment returned HTTP 200 with `status:"connected"`; reopening it in headed Chrome sent an offer with HTTP 200 and restored the second capture process. This verifies close cleanup and safe reconnect/replacement, but video still did not render.
- Navigating the owner Chrome profile to the Dashboard and clicking `Stop` changed the instance to `stopped`. The instance API then returned `runtimeHandle:null`; `adb devices -l` was empty and no `screenrecord` process remained. This verifies instance-stop cleanup for the active owner and observer captures.
- Final retry verification command `cd apps/worker && go test ./... && cd ../controlplane && npm test -- --run && cd ../admin && npm test && npm run build` passed: Worker **44 tests in 10 packages**, Control Plane **77 tests in 15 files**, and Admin **3 tests in 1 file** plus a successful build. The existing Admin 1,010.37 kB chunk warning and React Router future-flag warnings were non-failing.

### Retry acceptance results

| Check | Date | Command/evidence | Expected result | Actual result | Status |
| --- | --- | --- | --- | --- | --- |
| ADB console serial | 2026-09-20 | Worker-managed instance; `adb -s emulator-5600 get-state` | The Worker-selected ADB serial reaches its emulator. | Returned `device`; Android boot completed and Launcher was focused. | Pass |
| Create owner session | 2026-09-20 | Owner Chrome `Launch Session`; browser performance entry for `POST /api/v1/sessions` | HTTP 201 with a connected owner attachment. | `responseStatus:201`; Chrome rendered `owner` / `you`. | Pass |
| Owner video and input | 2026-09-20 | Owner Chrome video inspection after signaling HTTP 200 | Android video arrives; owner input visibly affects Android. | Chrome showed `live`, but video had zero dimensions and no tracks. No visible browser-video input test was possible. | Fail |
| Observer video and no input | 2026-09-20 | Controlled observer attachment in second headed Chrome; signaling HTTP 200; synthetic mouse attempt | Observer receives video; input is rejected and Android is unchanged. | Chrome showed `live`, but video had zero dimensions and no tracks. Android remained on Launcher after the observer attempt; Worker-side rejection was not directly observable. | Fail |
| Promote | 2026-09-20 | Owner Chrome `Promote`; observer role poll; synthetic participant mouse attempt | Observer becomes participant and input visibly affects Android. | Both Chrome profiles reflected `participant`; Android did not visibly change and no rendered video existed. | Fail |
| Demote | 2026-09-20 | Owner Chrome `Demote`; observer role poll; synthetic observer mouse attempt | Observer returns to observer and input is rejected. | Both Chrome profiles reflected `observer`; Android remained on Launcher. Worker-side rejection was not directly observable. | Partial |
| Tab close cleanup | 2026-09-20 | Close observer Chrome page; inspect session status and `screenrecord` process count | Attachment adapter/capture stops. | Attachment became `disconnected`; capture count dropped from two to one. | Pass |
| Reconnect | 2026-09-20 | Resume same observer attachment (HTTP 200), reopen in headed Chrome, inspect offer and capture count | Safe replacement transport supplies video without competing capture. | Signaling returned HTTP 200 and capture count returned to two; video remained absent. | Partial |
| Instance stop cleanup | 2026-09-20 | Owner Dashboard `Stop`; instance API; `adb devices -l`; `pgrep -alf screenrecord` | Instance adapters are removed and no capture process remains. | State was `stopped` with `runtimeHandle:null`; ADB device list and capture processes were empty. | Pass |

## Video-negotiation retry after `4c250e1`

**Date:** 2026-09-20. This clean retry rebuilt Admin and Worker, used a new Worker-managed AVD and isolated Control Plane database, and stopped at media root-cause evidence when the owner media connection failed. No observer, input, promotion, demotion, or reconnect claim below was inferred from this retry.

### Observed media boundary evidence

- A new `task6-video-20260920` instance was created and started through the headed owner Chrome Admin UI. Its Worker runtime handle used console port `5600`; `adb -s emulator-5600 get-state` returned `device` after `sys.boot_completed=1`.
- Owner Chrome `Launch Session` reached the session page and Worker started `adb -s emulator-5600 exec-out screenrecord --output-format=h264 --time-limit=0 -`.
- Chrome WebRTC Internals captured both SDP descriptions. The offer contained `m=video ...` with `a=mid:0`, `a=recvonly`, and H264 payload mappings (including `a=rtpmap:102 H264/90000`). The answer contained `m=video ...` with `a=mid:0`, `a=sendonly`, and the matching H264 payload mapping. This confirms video m-section negotiation after `4c250e1`.
- Redacted SDP candidate evidence: offer had four `host`/`srflx` candidates; answer had six `host`/`srflx` candidates. Chrome WebRTC Internals then recorded `onsignalingstatechange:"stable"`, `oniceconnectionstatechange:"failed"`, and `onconnectionstatechange:"failed"`. The candidate-pair grid was empty and its statistics entry count was zero.
- Chrome received a video track object (`srcObject` track count 1), but the video element remained `readyState:0`, `videoWidth:0`, `videoHeight:0`, and `paused:true`; no frame or visible Android video was observed. This is the exact stop boundary for the retry: SDP video negotiation and Android capture existed, but ICE never established a candidate pair or media statistics.
- The owner Dashboard `Stop` mouse attempt did not change the observed instance state, so cleanup was performed through the local Control Plane stop endpoint. It returned `desiredState:"stopped"`, `observedState:"stopped"`, and `runtimeHandle:null`; after a short wait `adb devices -l` was empty and no `screenrecord` process remained.
- Final verification command `cd apps/worker && go test ./... && cd ../controlplane && npm test -- --run && cd ../admin && npm test && npm run build` passed: Worker **45 tests in 10 packages**, Control Plane **77 tests in 15 files**, and Admin **4 tests in 1 file** plus a successful build. The existing Admin large-chunk and React Router future-flag warnings were non-failing.

### Video-negotiation retry results

| Check | Date | Expected result | Actual result | Status |
| --- | --- | --- | --- | --- |
| SDP video negotiation | 2026-09-20 | Offer and answer contain compatible `m=video` sections. | Both descriptions contained `m=video`; offer was `recvonly`, answer was `sendonly`, and both advertised H264. | Pass |
| ICE candidate connection | 2026-09-20 | A nominated candidate pair reaches connected state. | Host/srflx candidates were present, but Chrome reported ICE and connection `failed`, with empty candidate-pair grid and no stats. | Fail |
| Owner media | 2026-09-20 | Chrome renders Android video and permits visible input testing. | `srcObject` had one video track but no decoded frames or dimensions; no visible video. | Fail |
| Observer/input/role/reconnect | 2026-09-20 | Execute after owner media succeeds. | Not attempted after the verified owner ICE/media failure. | Blocked |
| Instance stop cleanup | 2026-09-20 | Stop removes AVD and capture processes. | Control Plane stop completed; ADB device list and `screenrecord` process list became empty. | Pass |

## Controlled ICE diagnostic retry

**Date:** 2026-09-20. This was one clean diagnostic run after `4c250e1`; it made no product-code changes and did not retry media behavior after capturing the ICE boundary.

### SDP candidate evidence

The final non-trickle offer and answer both retained `m=video` as recorded above. The following complete candidate lines were captured from Chrome WebRTC Internals; any `ufrag` or password field would be redacted (none appeared in these candidate lines).

**Offer — `a=end-of-candidates` absent; `.local` host candidates present:**

```text
a=candidate:2369011004 1 udp 2113937151 e68cc14e-dcfc-4d3f-b273-71e8aab1835f.local 61112 typ host generation 0 network-cost 999
a=candidate:2158352685 1 udp 1677729535 216.113.165.51 31519 typ srflx raddr 0.0.0.0 rport 0 generation 0 network-cost 999
a=candidate:2369011004 1 udp 2113937151 e68cc14e-dcfc-4d3f-b273-71e8aab1835f.local 65079 typ host generation 0 network-cost 999
a=candidate:2158352685 1 udp 1677729535 216.113.165.51 45381 typ srflx raddr 0.0.0.0 rport 0 generation 0 network-cost 999
```

**Answer — `a=end-of-candidates` absent; private IPv4 host candidates, no `.local`:**

```text
a=candidate:728098642 1 udp 2130706431 172.16.50.66 59404 typ host generation 0
a=candidate:728098642 2 udp 2130706431 172.16.50.66 59404 typ host generation 0
a=candidate:815991398 1 udp 2130706431 10.51.128.184 58717 typ host generation 0
a=candidate:815991398 2 udp 2130706431 10.51.128.184 58717 typ host generation 0
a=candidate:3605095043 1 udp 1694498815 216.113.165.51 64543 typ srflx raddr 0.0.0.0 rport 50754 generation 0
a=candidate:3605095043 2 udp 1694498815 216.113.165.51 64543 typ srflx raddr 0.0.0.0 rport 50754 generation 0
```

### Chrome state and statistics evidence

- Immediately after applying the remote answer, Chrome emitted `iceconnectionstatechange: checking` and `connectionstatechange: connecting`.
- Chrome WebRTC Internals later recorded `onsignalingstatechange:"stable"`, `oniceconnectionstatechange:"failed"`, and `onconnectionstatechange:"failed"`.
- The final page-level states were `signalingState:"stable"`, `iceConnectionState:"disconnected"`, and `connectionState:"failed"`.
- Final `getStats()` contained five local-candidate entries and one transport entry. The local candidates were two UDP host ports (`51381`, `61112`), two TCP host port-9 entries, and one UDP server-reflexive candidate (`216.113.165.51:31519`); Chrome exposed blank host addresses in this stats view because the offer used mDNS. The transport reported `packetsReceived:0` and `bytesReceived:0`.
- No `candidate-pair`, `remote-candidate`, or `inbound-rtp` entry was returned by final `getStats()`. Chrome WebRTC Internals likewise had an empty candidate-pair grid and zero stored statistics entries. A same-run immediate-after-`setRemoteDescription` statistics snapshot was not retained; the initial state transition above and the final zero-traffic snapshot are the available non-sensitive evidence.
- The Worker had started its `adb ... screenrecord` process and Chrome held one video track, but Chrome rendered no frame. The instance was stopped through the Control Plane afterward; ADB became empty.

### Diagnostic result

| Check | Result |
| --- | --- |
| Video SDP negotiation | Pass: compatible H264 video sections were present. |
| Candidate gathering | Partial: candidates gathered, but offer host candidates were mDNS `.local` while answer host candidates were private IPv4; neither SDP had `a=end-of-candidates`. |
| ICE connectivity | Fail: no candidate pair, remote candidate, inbound RTP, or received bytes; final connection state failed. |
| Further media/role tests | Intentionally not retried after the ICE boundary was captured. |

## Controlled raw-host-candidate retry (mDNS disabled)

**Date:** 2026-09-20. This was one controlled diagnostic run with code, Worker, STUN service, Control Plane configuration, and network unchanged. The only deliberate client change was the dedicated owner Chrome launch argument `--disable-features=WebRtcHideLocalIpsWithMdns`.

### Result: mDNS candidate-interoperability root cause confirmed

The owner Chrome profile connected (`oniceconnectionstatechange:"completed"`, `onconnectionstatechange:"connected"`) and rendered Android video with `readyState:4`, `videoWidth:1080`, and `videoHeight:2400`. Chrome WebRTC Internals recorded 284 statistics entries, including candidate-pair, transport, and inbound-RTP series. In contrast, the immediately preceding identical controlled retry with mDNS enabled failed before creating a candidate pair. This controlled one-variable result confirms that mDNS host-candidate interoperability was the ICE failure boundary in this local flow.

### Candidate evidence

**Offer — raw private IPv4 host candidates, no `.local`, `a=end-of-candidates` absent:**

```text
a=candidate:2768697515 1 udp 2113937151 172.16.50.66 62962 typ host generation 0 network-cost 999
a=candidate:2828475578 1 udp 1677729535 216.113.165.51 59165 typ srflx raddr 172.16.50.66 rport 62962 generation 0 network-cost 999
a=candidate:2768697515 1 udp 2113937151 172.16.50.66 62943 typ host generation 0 network-cost 999
a=candidate:2828475578 1 udp 1677729535 216.113.165.51 3573 typ srflx raddr 172.16.50.66 rport 62943 generation 0 network-cost 999
```

**Answer — raw private IPv4 host candidates, no `.local`, `a=end-of-candidates` absent:**

```text
a=candidate:728098642 1 udp 2130706431 172.16.50.66 63358 typ host generation 0
a=candidate:728098642 2 udp 2130706431 172.16.50.66 63358 typ host generation 0
a=candidate:815991398 1 udp 2130706431 10.51.128.184 55136 typ host generation 0
a=candidate:815991398 2 udp 2130706431 10.51.128.184 55136 typ host generation 0
a=candidate:3605095043 1 udp 1694498815 216.113.165.51 47007 typ srflx raddr 0.0.0.0 rport 58092 generation 0
a=candidate:3605095043 2 udp 1694498815 216.113.165.51 47007 typ srflx raddr 0.0.0.0 rport 58092 generation 0
```

The instance was then stopped; ADB devices and screen-capture processes were cleaned up. This diagnostic stops here and does not extend to observer, role, or reconnect claims.

## Automated verification

Run the required suite before and after acceptance. Record exit status and any registry or authentication failure from `npm ci` verbatim in the results table or execution notes.

```bash
cd apps/worker && go test ./...
cd ../controlplane && npm ci && npm test -- --run && npm run build
cd ../admin && npm ci && npm test && npm run build
cd ../.. && make build

# Final required checks
cd apps/worker && go test ./... && \
  cd ../controlplane && npm test -- --run && \
  cd ../admin && npm test && npm run build
```

## Cleanup

Stop the Control Plane and Worker with `Ctrl-C`. Close both Chrome profiles and stop the instance if it is still running. Verify cleanup with:

```bash
adb devices -l
pgrep -alf 'mako-worker|screenrecord|emulator.*Medium_Phone_API_36.1' || true
```
