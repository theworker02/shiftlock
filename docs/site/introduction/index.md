# Introduction

ShiftLock is a **security-first runtime coordination and control module** for
production Go systems. It coordinates **ownership**, **fencing**, and (optionally)
a **runtime control plane** so sensitive work has a clear owner, a monotonic
fencing token, and a safe handoff path between instances.

It is not a hosted SaaS control plane, remote shell, or SIEM. Import it as a
normal module:

```go
import "github.com/theworker02/shiftlock"
```

| | |
|---|---|
| **Module** | [`github.com/theworker02/shiftlock`](https://pkg.go.dev/github.com/theworker02/shiftlock) |
| **Repository** | [github.com/theworker02/shiftlock](https://github.com/theworker02/shiftlock) |
| **Package docs** | [pkg.go.dev](https://pkg.go.dev/github.com/theworker02/shiftlock) |

## Design stance

- **Core stays lightweight** — stdlib-first; no third-party requires in the root
  `go.mod`.
- **Advanced packages are opt-in** — `capability/`, `guard/`, `audit/`, `control/`,
  `supervise/`, `election/`, `security/`, and resource/workflow adapters.
- **Fail closed when enabled** — privileged operations default to deny once
  security subsystems are turned on.
- **Coordinator APIs stay stable** — `shiftlock.New` and the Phase 5 Coordinator
  surface remain unchanged when you adopt Phase 6 runtime features.

## What ShiftLock decides

Graceful shutdown stops an old process. ShiftLock decides who may perform
protected work next — with fencing tokens so a stale process cannot keep acting
after losing ownership — and optionally extends that model to supervisors,
workflows, databases, queues, and APIs.

Continue with [Quick Start](../quick-start/index.md) or [Concepts](../concepts/index.md).
