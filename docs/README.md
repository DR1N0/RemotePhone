# Documentation index

- **[`architecture/`](architecture/overview.md)** — the current-state source of truth: system topology, data model, security model, API surfaces, and a decisions log. Start here. These files are living documents, edited in place as the system changes.
- **[`architecture/planned-subsystems.md`](architecture/planned-subsystems.md)** — design intent for what's not built yet (worker lifecycle, controller, networking, base images). A target, not current state — move sections out to the other architecture files as they're implemented.
- **[`testing/`](testing/local-transport-e2e.md)** — manual test procedures.
- **[`controlplane-deployment.md`](controlplane-deployment.md)** — deployment guide for the legacy TypeScript control plane (`apps/controlplane`). Does not apply to the Go implementation in `apps/server`, which has no deployment guide yet.
