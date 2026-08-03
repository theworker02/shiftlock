// Package resource provides ShiftLock's resource fabric foundation:
// typed resource identities, capability declarations, a Runtime-owned
// registry, dependency graphs, bundles, health, and monotonic resource epochs.
//
// Adapters live in subpackages (e.g. resource/memory). Optional heavy backends
// must stay isolated from core — this package uses the Go standard library only.
//
// Capability honesty: never claim Supports* flags a concrete resource cannot
// honor. Lockdown, when wired by Runtime, blocks protected mutations.
package resource
