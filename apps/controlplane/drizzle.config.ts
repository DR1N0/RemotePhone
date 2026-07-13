import { defineConfig } from "drizzle-kit"

export default defineConfig({
  dialect: "sqlite",
  schema: "./src/persistence/schema.ts",
  out: "./src/persistence/migrations",
  dbCredentials: {
    url: "./data/mako.db",
  },
})
