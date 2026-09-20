# Local Transport End-to-End Acceptance Runbook

This runbook records the real local transport acceptance flow. It is intentionally limited to macOS, Android Studio Emulator, and Google Chrome on localhost or a controlled LAN.

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

- Google Chrome. Use two separate Chrome profiles/windows: one owner and one observer.
- Go 1.24 or later (the Worker module declares `go 1.24`).
- Node.js and npm compatible with the checked-in lockfiles. The execution environment must record the exact versions below.
- A controlled network: `127.0.0.1` for a single-machine run, or a trusted LAN with the Worker and Control Plane explicitly bound and addressed for that LAN. Public NAT traversal/TURN is not part of this flow.
- Available local ports: Worker `127.0.0.1:7780`, Control Plane `127.0.0.1:3000`, and the Chrome remote-debugging ports used below (`9222` and `9223`).

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
# Build the Admin before launching the Control Plane; it serves apps/admin/dist at /admin.
cd apps/admin && npm ci && npm run build

# Terminal 1: local Worker. The explicit SDK settings make Emulator discovery repeatable.
cd apps/worker
MAKO_ANDROID_SDK_ROOT="$HOME/Library/Android/sdk" \
MAKO_ANDROID_EMULATOR_PATH="$HOME/Library/Android/sdk/emulator/emulator" \
MAKO_WORKER_LISTEN_ADDR="127.0.0.1:7780" \
MAKO_WORKER_BASE_URL="http://127.0.0.1:7780" \
go run ./cmd/mako-worker

# Terminal 2: Control Plane (after the Admin build).
cd apps/controlplane
MAKO_CONTROLPLANE_PORT="3000" \
MAKO_WORKER_BASE_URL="http://127.0.0.1:7780" \
npm run dev

# Terminal 3: verify services, then open the Admin in an actual headed Google Chrome owner profile.
curl -fsS http://127.0.0.1:7780/healthz
curl -fsS http://127.0.0.1:3000/healthz
open -na "Google Chrome" --args \
  --user-data-dir=/tmp/mako-chrome-owner \
  --remote-debugging-port=9222 \
  http://127.0.0.1:3000/admin
```

For the observer, create and copy an invite from the owner session, then open that invite URL in a distinct Chrome profile:

```bash
open -na "Google Chrome" --args \
  --user-data-dir=/tmp/mako-chrome-observer \
  --remote-debugging-port=9223 \
  '<copied-invite-url>'
```

## Acceptance procedure

1. Start a real AVD (`Medium_Phone_API_36.1` for this run) and wait until `adb devices -l` reports its serial as `device`; wait for the Android home screen.
2. Build the Admin, start the Worker, then start the Control Plane using the commands above. Confirm `/admin` loads in the owner Chrome profile.
3. Create an instance from the Android Studio Emulator template, start it, and wait for the Android home screen to appear in the owner Chrome video element.
4. Click or tap a visible Android home-screen element in the owner video. Verify the corresponding visible change in the emulator window and/or `adb` state.
5. Create an invite and open it in the observer Chrome profile. Verify its video arrives. Attempt input and verify the Worker rejects it and Android does not change.
6. In the owner window, promote the observer. Verify observer input changes Android. Demote the same attachment and verify input is once again rejected.
7. Close the observer tab/window. Confirm the Worker adapter/capture stops; if immediate inspection is unavailable, reconnect that attachment and confirm a safe replacement adapter creates video without a competing capture.
8. Stop the instance from the owner dashboard. Confirm all of its adapters are removed and no Mako screen-capture process remains.

## Execution notes

- **2026-09-20 13:20:15 CST — AVD readiness: PASS.** Command: `"$HOME/Library/Android/sdk/emulator/emulator" -avd Medium_Phone_API_36.1 -no-snapshot -no-boot-anim`, followed by `adb devices -l`, `adb -s emulator-5554 shell getprop sys.boot_completed`, and `adb -s emulator-5554 shell dumpsys window`. Actual result: `emulator-5554` reported `device`, `sys.boot_completed=1`, and the focused activity was `com.google.android.apps.nexuslauncher/.NexusLauncherActivity` (Android home screen).
- **2026-09-20 13:20–13:21 CST — Services and headed Chrome Admin: PASS.** The Worker `/healthz` returned `{"status":"ok","workerId":"local","hostOs":"macos","arch":"arm64","installedRuntimes":[{"runtimeClass":"android-studio-emulator","displayName":"Android Studio Emulator"}]}`. The Control Plane `/healthz` returned `{"status":"ok"}` and `GET /admin` returned `HTTP/1.1 200 OK` with `content-type: text/html; charset=utf-8`. A headed Google Chrome 152 owner profile opened `/admin`; its rendered body contained `mako admin`, `Create Instance`, and the instance table.
- **2026-09-20 13:23:10–13:24:40 CST — Worker-managed AVD: PASS after an initial environmental conflict.** Creating the dedicated `task6-transport-20260920` instance then starting it while the manually started copy of the same AVD was running failed with `FATAL | Running multiple emulators with the same AVD is an experimental feature.Please use -read-only flag to enable this feature.` The manual AVD was stopped with `adb -s emulator-5554 emu kill`; restarting the dedicated instance then reached Control Plane `observedState:"running"`, `adb` serial `emulator-5600` in state `device`, `sys.boot_completed=1`, and the Android launcher focused.
- **2026-09-20 13:25:xx CST — Transport ADB serial blocker: FAIL.** The instance assigned `adbPort:5601`, but the Worker transport adapter derives the serial `localhost:5601`. The exact probe `adb -s localhost:5601 get-state` returned `error: device 'localhost:5601' not found`, while the running emulator was available as `emulator-5600`. This prevents the required Worker-side touch forwarding check from succeeding in this environment.
- **2026-09-20 13:21–13:26 CST — Chrome/session automation blocker: BLOCKED.** The initial headed Chrome Admin tab was available through remote debugging, but the remote-debugging endpoint subsequently failed with `curl: (7) Failed to connect to 127.0.0.1 port 9222 after 0 ms: Couldn't connect to server`; the Chrome main process had restarted with `--restart`. A direct session creation attempt returned HTTP 500 to the initial `curl -fsS` call; its immediate replay returned `HTTP/1.1 409 Conflict` with `{"error":"active session already exists for instance 9eff0787-290d-4c2a-a89c-76b3762c5e36"}`. The documented API exposes no session-list route, so the active attachment could not be recovered through supported public routes to complete a real owner/observer Chrome sequence. No result below is marked successful on the basis of this partial setup.
- **2026-09-20 13:26:53 CST — Instance stop runtime cleanup: PARTIAL.** `POST /api/v1/instances/9eff0787-290d-4c2a-a89c-76b3762c5e36/stop` returned `desiredState:"stopped"`, `observedState:"stopped"`, and `runtimeHandle:null`. A subsequent `adb devices -l` was empty, and no `screenrecord`/Mako capture process was present. Because no browser attachment transport was successfully established, this does not verify removal of active adapters.
- **2026-09-20 13:18–13:20 CST — Required automated verification: PASS.** `cd apps/worker && go test ./...` passed **43 tests in 10 packages**. `cd apps/controlplane && npm ci && npm test -- --run && npm run build` passed **15 files / 76 tests** and built successfully; `npm ci` reached the configured registry without an authentication failure and reported **13 vulnerabilities (8 moderate, 4 high, 1 critical)**. `cd apps/admin && npm ci && npm test && npm run build` passed **1 file / 3 tests** and built successfully; `npm ci` reached the configured registry without an authentication failure and reported **9 vulnerabilities (6 moderate, 2 high, 1 critical)**. `make build` passed. Both Admin production builds emitted the existing Vite warning that the minified JavaScript chunk is **1,010.37 kB**, above the 500 kB warning threshold; this did not change their exit status.
- **2026-09-20 13:27 CST — Final required checks: PASS.** `cd apps/worker && go test ./... && cd ../controlplane && npm test -- --run && cd ../admin && npm test && npm run build` passed: Worker **43 tests in 10 packages**, Control Plane **15 files / 76 tests**, and Admin **1 file / 3 tests** plus a successful build. The same existing Admin large-chunk warning and React Router future-flag warnings were emitted without failing the command.

## Results

Use the exact commands and observations below. `Not run` is not a pass.

| Check | Date | Command | Expected result | Actual result | Status |
| --- | --- | --- | --- | --- | --- |
| Owner video and input | 2026-09-20 | Headed Chrome owner `/admin` attempt; `POST /api/v1/sessions` for instance `9eff0787-290d-4c2a-a89c-76b3762c5e36`; `adb -s localhost:5601 get-state` | Android video arrives; owner input visibly affects the emulator. | The Admin dashboard rendered in real Chrome, but the Chrome debug endpoint restarted before its session UI could be driven. The first session-create command returned 500, its replay returned 409 active-session conflict, and `adb -s localhost:5601 get-state` returned `error: device 'localhost:5601' not found`. No video or forwarded owner input was observed. | Fail — blocked |
| Observer video and no input | 2026-09-20 | Observer Chrome invite flow and input attempt from step 5 | Observer receives video; Worker rejects input; Android is unchanged. | Blocked before an invite URL could be obtained from a recoverable active owner session. | Fail — blocked |
| Promote | 2026-09-20 | Owner `Promote` action, then observer input from step 6 | Observer becomes participant and its input visibly affects Android. | Blocked because no observer attachment could be created; Worker ADB serial probe also failed. | Fail — blocked |
| Demote | 2026-09-20 | Owner `Demote` action, then observer input from step 6 | Observer returns to observer role; Worker rejects its input; Android is unchanged. | Blocked because no observer attachment could be created; Worker ADB serial probe also failed. | Fail — blocked |
| Tab close cleanup | 2026-09-20 | Close observer Chrome tab/window, inspect Worker/capture or reconnect from step 7 | Attachment adapter/capture stops, or reconnect safely replaces it. | Blocked because no observer transport was established. | Fail — blocked |
| Reconnect | 2026-09-20 | Reopen the observer invite after closure from step 7 | A fresh safe transport supplies video without competing capture. | Blocked because no observer invite/transport was established. | Fail — blocked |
| Instance stop cleanup | 2026-09-20 | `POST /api/v1/instances/9eff0787-290d-4c2a-a89c-76b3762c5e36/stop`; `adb devices -l`; `pgrep -alf 'screenrecord|mako-worker.*capture'` | Instance adapters are removed and no Mako screen capture process remains. | Control Plane reported `stopped` with `runtimeHandle:null`; subsequent `adb devices -l` was empty and no capture process remained. No active adapter existed to prove adapter removal. | Fail — partial |

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
