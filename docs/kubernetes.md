# Kubernetes

The `backend/kubernetes` package maps ShiftLock claims onto
`coordination.k8s.io/v1` Lease objects.

## Design

- `HolderIdentity` = generation ID
- `leaseDurationSeconds` / `renewTime` = lease TTL + heartbeat
- Annotations carry fencing token, phase, successor, and reason
- Optimistic concurrency via `resourceVersion`

## Wiring a real client

Implement `LeaseClient` with client-go:

```go
// Pseudocode
lease, err := client.CoordinationV1().Leases(ns).Get(ctx, name, metav1.GetOptions{})
```

Keep this adapter in your service binary so `github.com/theworker02/shiftlock`
itself never imports Kubernetes libraries.

## RBAC

Grant `get`, `create`, `update` on `leases` in the target namespace.
