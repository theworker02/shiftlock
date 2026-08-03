# ShiftLock Docs

ShiftLock is a **security-first runtime coordination and control module** for
production Go systems. It protects who may perform sensitive work, when that
work may run, and how responsibility moves safely between instances.

Start with [Quick Start](quick-start/index.md), then [Concepts](concepts/index.md).

!!! warning "Opt-in security"
    Advanced control-plane features are **opt-in**. `shiftlock.New` / Coordinator
    APIs remain unchanged. Deny privileged operations by default when security
    subsystems are enabled.
