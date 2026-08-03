# ShiftLock

![ShiftLock](assets/shiftlock-horizontal.svg){: .sl-hero-logo }

**Security-first runtime coordination and control for Go.**

ShiftLock protects who may perform sensitive work, when that work may run, and
how responsibility moves safely between instances — without a hosted control
plane.

<div class="sl-links" markdown>

[Quick Start](quick-start/index.md) ·
[Introduction](introduction/index.md) ·
[Go package docs](https://pkg.go.dev/github.com/theworker02/shiftlock) ·
[GitHub](https://github.com/theworker02/shiftlock)

</div>

!!! warning "Opt-in security"
    Advanced control-plane features are **opt-in**. `shiftlock.New` / Coordinator
    APIs remain unchanged. Deny privileged operations by default when security
    subsystems are enabled.

## What it is

| It is | It is not |
|-------|-----------|
| Ownership handoff + fencing for Go processes | A hosted control plane |
| An opt-in runtime supervisor & security layer | A Kubernetes-only framework |
| A shared fabric around DBs, queues, APIs, files | A replacement for those systems |
| Importable as a normal Go module | A SaaS product |

```bash
go get github.com/theworker02/shiftlock@latest
```

Module path: [`github.com/theworker02/shiftlock`](https://pkg.go.dev/github.com/theworker02/shiftlock).

## Next steps

1. [Introduction](introduction/index.md) — positioning and package layout
2. [Quick Start](quick-start/index.md) — install, run examples, first coordinator
3. [Concepts](concepts/index.md) — claims, fencing, capabilities, lockdown
4. [API Reference](api-reference/index.md) — packages on pkg.go.dev
