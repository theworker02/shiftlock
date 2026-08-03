# Quick Start

```bash
go get github.com/theworker02/shiftlock@latest
go run ./examples/singleton-worker
go run ./examples/secure-control-plane
```

Coordinator (Phase 5):

```go
coord, err := shiftlock.New(shiftlock.Config{
    Service: "billing", InstanceID: "pod-a", Backend: memory.New(), LeaseTTL: 15 * time.Second,
})
```

Runtime (Phase 6, opt-in):

```go
rt, err := shiftlock.NewRuntime(shiftlock.RuntimeConfig{
    Config: shiftlock.Config{Service: "billing", InstanceID: "pod-a", Backend: be, LeaseTTL: 15 * time.Second},
    SecurityProfile: shiftlock.ProfileStandard,
    EnableSupervisor: true, EnableAudit: true, EnableCapabilities: true, EnableLockdown: true,
})
defer rt.Close()
```
