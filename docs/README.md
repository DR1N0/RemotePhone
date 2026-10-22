# Documentation index

- **[`architecture/`](architecture/overview.md)** — the current-state source of truth: system topology, data model, security model, API surfaces, and a decisions log. Start here. These files are living documents, edited in place as the system changes.
- **[`plans/`](plans/roadmap.md)** — the phase roadmap and per-phase plans (historical, kept for rationale and verified acceptance criteria).
- **[`architecture/planned-subsystems.md`](architecture/planned-subsystems.md)** — design intent for what's not built yet. A target, not current state — move sections out to the other architecture files as they're implemented.
- **[`testing/`](testing/local-transport-e2e.md)** — manual test procedures.
- **[`windows-migration-handoff.md`](windows-migration-handoff.md)** — Windows host verification results, including the AMD AMF finding and the still-open real-phone LAN test.
- **[`controlplane-deployment.md`](controlplane-deployment.md)** — deployment guide for the legacy TypeScript control plane (`apps/controlplane`). Does not apply to the Go implementation in `apps/server`; Go build/run steps live in the top-level README.
