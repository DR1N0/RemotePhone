import { describe, expect, it, vi } from "vitest"

import { LocalWorkerClient } from "../src/services/worker-client.js"

describe("local worker client", () => {
  it("maps the worker health response into a public worker record", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      status: "ok",
      workerId: "local",
      hostOs: "macos",
      arch: "arm64",
      installedRuntimes: [
        {
          runtimeClass: "android-studio-emulator",
          displayName: "Android Studio Emulator",
        },
      ],
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }))

    const client = new LocalWorkerClient("http://127.0.0.1:7780", fetchImpl as typeof fetch)

    await expect(client.listWorkers()).resolves.toEqual([
      {
        id: "local",
        kind: "local",
        hostOs: "macos",
        arch: "arm64",
        health: "ok",
        installedRuntimes: [
          {
            runtimeClass: "android-studio-emulator",
            displayName: "Android Studio Emulator",
          },
        ],
      },
    ])
    expect(fetchImpl).toHaveBeenCalledWith("http://127.0.0.1:7780/healthz")
  })

  it("returns templates when the worker template endpoint responds with a raw array", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify([
      {
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        displayName: "Medium Phone",
        metadata: { avdName: "Medium_Phone" },
      },
    ]), {
      status: 200,
      headers: { "content-type": "application/json" },
    }))

    const client = new LocalWorkerClient("http://127.0.0.1:7780", fetchImpl as typeof fetch)

    await expect(client.listTemplates()).resolves.toEqual([
      {
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        displayName: "Medium Phone",
        metadata: { avdName: "Medium_Phone" },
      },
    ])
    expect(fetchImpl).toHaveBeenCalledWith("http://127.0.0.1:7780/v1/templates")
  })

  it("returns templates when the worker template endpoint responds with a wrapped templates object", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      templates: [
        {
          templateId: "macos-android-studio:Medium_Phone",
          runtimeClass: "android-studio-emulator",
          displayName: "Medium Phone",
          metadata: { avdName: "Medium_Phone" },
        },
      ],
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }))

    const client = new LocalWorkerClient("http://127.0.0.1:7780", fetchImpl as typeof fetch)

    await expect(client.listTemplates()).resolves.toEqual([
      {
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        displayName: "Medium Phone",
        metadata: { avdName: "Medium_Phone" },
      },
    ])
    expect(fetchImpl).toHaveBeenCalledWith("http://127.0.0.1:7780/v1/templates")
  })

  it("starts an instance through the worker start endpoint", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      state: "starting",
      handle: {
        instanceId: "instance-1",
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: 4321,
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }))

    const client = new LocalWorkerClient("http://127.0.0.1:7780", fetchImpl as typeof fetch)

    await expect(client.startInstance({
      instanceId: "instance-1",
      runtimeClass: "android-studio-emulator",
      templateId: "macos-android-studio:Medium_Phone",
      launchConfig: {
        headless: true,
        readOnly: true,
        wipeData: false,
      },
    })).resolves.toEqual({
      state: "starting",
      handle: {
        instanceId: "instance-1",
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: 4321,
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    })

    expect(fetchImpl).toHaveBeenCalledWith(
      "http://127.0.0.1:7780/v1/instances/start",
      expect.objectContaining({
        method: "POST",
        headers: { "content-type": "application/json" },
      }),
    )
  })

  it("stops an instance through the worker stop endpoint", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      state: "stopped",
      handle: {
        instanceId: "instance-1",
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: 4321,
      },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }))

    const client = new LocalWorkerClient("http://127.0.0.1:7780", fetchImpl as typeof fetch)

    await expect(client.stopInstance("instance-1")).resolves.toEqual({
      state: "stopped",
      handle: {
        instanceId: "instance-1",
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: 4321,
      },
    })

    expect(fetchImpl).toHaveBeenCalledWith(
      "http://127.0.0.1:7780/v1/instances/instance-1/stop",
      expect.objectContaining({
        method: "POST",
      }),
    )
  })

  it("creates a transport session through the worker transport endpoint", async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      sessionId: "session-1",
      attachmentId: "attachment-1",
      instanceId: "instance-1",
      runtimeClass: "android-studio-emulator",
      mode: "prototype-webrtc",
      status: "stub",
      workerBaseUrl: "http://127.0.0.1:7780",
      sessionToken: "token-1",
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }))

    const client = new LocalWorkerClient("http://127.0.0.1:7780", fetchImpl as typeof fetch)

    await expect(client.createTransportSession({
      sessionId: "session-1",
      attachmentId: "attachment-1",
      instanceId: "instance-1",
      runtimeClass: "android-studio-emulator",
      runtimeHandle: {
        workerId: "local",
        pid: 4321,
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    })).resolves.toEqual({
      sessionId: "session-1",
      attachmentId: "attachment-1",
      instanceId: "instance-1",
      runtimeClass: "android-studio-emulator",
      mode: "prototype-webrtc",
      status: "stub",
      workerBaseUrl: "http://127.0.0.1:7780",
      sessionToken: "token-1",
    })

    expect(fetchImpl).toHaveBeenCalledWith(
      "http://127.0.0.1:7780/v1/transport/session",
      expect.objectContaining({
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          sessionId: "session-1",
          attachmentId: "attachment-1",
          instanceId: "instance-1",
          runtimeClass: "android-studio-emulator",
          runtimeHandle: {
            workerId: "local",
            pid: 4321,
            consolePort: 5600,
            adbPort: 5601,
            grpcPort: 8554,
          },
        }),
      }),
    )
  })
})
