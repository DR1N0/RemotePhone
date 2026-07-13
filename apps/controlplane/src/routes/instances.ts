import type { Context, Hono } from "hono"

import { createInstanceRequestSchema } from "../domain/schemas.js"
import {
  InstanceNotFoundError,
  InvalidRequestError,
  type InstanceService,
} from "../services/instance-service.js"

function errorStatus(error: unknown): 400 | 404 | 500 {
  if (error instanceof InvalidRequestError) {
    return 400
  }
  if (error instanceof InstanceNotFoundError) {
    return 404
  }
  return 500
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message
  }
  return "unexpected error"
}

function jsonError(c: Context, error: unknown) {
  c.status(errorStatus(error))
  return c.json({ error: errorMessage(error) })
}

export function registerInstanceRoutes(app: Hono, instanceService: InstanceService): void {
  app.post("/api/v1/instances", async (c) => {
    try {
      const input = createInstanceRequestSchema.parse(await c.req.json())
      const instance = await instanceService.createManagedInstance(input)
      c.status(201)
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.get("/api/v1/instances", async (c) => {
    try {
      const instances = await instanceService.listManagedInstances()
      return c.json({ instances })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.get("/api/v1/instances/:id", async (c) => {
    try {
      const instance = await instanceService.getManagedInstance(c.req.param("id"))
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/instances/:id/start", async (c) => {
    try {
      const instance = await instanceService.startManagedInstance(c.req.param("id"))
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/instances/:id/stop", async (c) => {
    try {
      const instance = await instanceService.stopManagedInstance(c.req.param("id"))
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.delete("/api/v1/instances/:id", async (c) => {
    try {
      await instanceService.deleteManagedInstance(c.req.param("id"))
      return c.body(null, 204)
    } catch (error) {
      return jsonError(c, error)
    }
  })
}
