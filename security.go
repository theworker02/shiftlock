package shiftlock

import (
	"fmt"
	"time"
)

// Principal identifies an actor for authorization and audit.
type Principal struct {
	ID         string            `json:"id"`
	Kind       PrincipalKind     `json:"kind"`
	Generation string            `json:"generation,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"`
}

// PrincipalKind classifies principals.
type PrincipalKind string

const (
	PrincipalService    PrincipalKind = "service"
	PrincipalOperator   PrincipalKind = "operator"
	PrincipalGeneration PrincipalKind = "generation"
	PrincipalSystem     PrincipalKind = "system"
	PrincipalCapability PrincipalKind = "capability"
)

// Permission is a stable authorization unit. Privileged permissions are denied
// unless explicitly granted via capability / guard policy.
type Permission string

const (
	PermClaimAcquire      Permission = "claim.acquire"
	PermClaimRelease      Permission = "claim.release"
	PermClaimForceRelease Permission = "claim.force_release"
	PermTaskStart         Permission = "task.start"
	PermTaskStop          Permission = "task.stop"
	PermTaskRestart       Permission = "task.restart"
	PermMaintenanceEnter  Permission = "maintenance.enter"
	PermMaintenanceExit   Permission = "maintenance.exit"
	PermLockdownEnter     Permission = "lockdown.enter"
	PermLockdownExit      Permission = "lockdown.exit"
	PermCommandInvoke     Permission = "command.invoke"
	PermCommandRegister   Permission = "command.register"
	PermCapabilityIssue   Permission = "capability.issue"
	PermCapabilityRevoke  Permission = "capability.revoke"
	PermElectionJoin      Permission = "election.join"
	PermElectionResign    Permission = "election.resign"
	PermAuditRead         Permission = "audit.read"
	PermExecRun           Permission = "exec.run"
	PermQuorumVote        Permission = "quorum.vote"
	PermRecoveryAct       Permission = "recovery.act"
)

// SecurityEpoch is a monotonic authorization epoch. It must never decrease or
// wrap silently; overflow is a terminal error.
type SecurityEpoch uint64

// MaxSecurityEpoch is the last valid epoch before terminal overflow.
const MaxSecurityEpoch SecurityEpoch = SecurityEpoch(^uint64(0) - 1)

// Next returns epoch+1 or ErrSecurityEpochOverflow.
func (e SecurityEpoch) Next() (SecurityEpoch, error) {
	if e >= MaxSecurityEpoch {
		return e, &Error{Op: "SecurityEpoch.Next", Err: ErrSecurityEpochOverflow, Code: CodeEpochOverflow, Category: CategorySecurity, Message: "security epoch overflow"}
	}
	return e + 1, nil
}

// SecurityProfile selects a named secure-defaults bundle.
type SecurityProfile string

const (
	ProfileDevelopment     SecurityProfile = "development"
	ProfileTesting         SecurityProfile = "testing"
	ProfileStandard        SecurityProfile = "standard"
	ProfileHardened        SecurityProfile = "hardened"
	ProfileMaximumSecurity SecurityProfile = "maximum-security"
)

// SecuritySettings are the expanded, inspectable security controls.
// Zero-value fields after ExpandSecurityProfile mean "use profile default".
type SecuritySettings struct {
	Profile SecurityProfile `json:"profile"`

	// DenyPrivilegedByDefault rejects privileged ops without an allow decision.
	DenyPrivilegedByDefault bool `json:"deny_privileged_by_default"`

	// RequireCapabilityForPrivileged requires a verified capability token.
	RequireCapabilityForPrivileged bool `json:"require_capability_for_privileged"`

	// AuditEnabled turns on hash-chained audit recording.
	AuditEnabled bool `json:"audit_enabled"`

	// AuditFailClosed refuses privileged ops if audit append fails.
	AuditFailClosed bool `json:"audit_fail_closed"`

	// AntiReplayEnabled enables bounded nonce/request-id cache checks.
	AntiReplayEnabled bool `json:"anti_replay_enabled"`

	// AntiReplayMaxEntries bounds the replay cache (required > 0 when enabled).
	AntiReplayMaxEntries int `json:"anti_replay_max_entries"`

	// CapabilityMaxTTL caps issued capability lifetime.
	CapabilityMaxTTL time.Duration `json:"capability_max_ttl"`

	// CapabilityDefaultTTL is used when Issue omits TTL.
	CapabilityDefaultTTL time.Duration `json:"capability_default_ttl"`

	// RequireSingleUseCapabilities forces single-use on issue when true.
	RequireSingleUseCapabilities bool `json:"require_single_use_capabilities"`

	// LockdownOnAuditTamper auto-enters lockdown when audit verify fails.
	LockdownOnAuditTamper bool `json:"lockdown_on_audit_tamper"`

	// LockdownOnSplitBrain auto-enters lockdown on split-brain detection.
	LockdownOnSplitBrain bool `json:"lockdown_on_split_brain"`

	// LockdownOnCommandFlood auto-enters lockdown after unauthorized flood.
	LockdownOnCommandFlood bool `json:"lockdown_on_command_flood"`

	// CommandFloodThreshold is unauthorized denials per window.
	CommandFloodThreshold int `json:"command_flood_threshold"`

	// CommandFloodWindow is the flood detection window.
	CommandFloodWindow time.Duration `json:"command_flood_window"`

	// AllowExec enables execguard (still allowlist-only). Default false.
	AllowExec bool `json:"allow_exec"`

	// QuarantineOnTamper marks the generation quarantined on security tamper.
	QuarantineOnTamper bool `json:"quarantine_on_tamper"`

	// MaxCommandBodyBytes limits command payloads.
	MaxCommandBodyBytes int `json:"max_command_body_bytes"`

	// DefaultCommandDeadline bounds command execution.
	DefaultCommandDeadline time.Duration `json:"default_command_deadline"`
}

// ExpandSecurityProfile returns explicit settings for a profile.
func ExpandSecurityProfile(p SecurityProfile) SecuritySettings {
	switch p {
	case ProfileDevelopment:
		return SecuritySettings{
			Profile:                        ProfileDevelopment,
			DenyPrivilegedByDefault:        true,
			RequireCapabilityForPrivileged: false,
			AuditEnabled:                   false,
			AuditFailClosed:                false,
			AntiReplayEnabled:              false,
			AntiReplayMaxEntries:           256,
			CapabilityMaxTTL:               time.Hour,
			CapabilityDefaultTTL:           15 * time.Minute,
			RequireSingleUseCapabilities:   false,
			LockdownOnAuditTamper:          false,
			LockdownOnSplitBrain:           false,
			LockdownOnCommandFlood:         false,
			CommandFloodThreshold:          100,
			CommandFloodWindow:             time.Minute,
			AllowExec:                      false,
			QuarantineOnTamper:             false,
			MaxCommandBodyBytes:            1 << 20,
			DefaultCommandDeadline:         30 * time.Second,
		}
	case ProfileTesting:
		return SecuritySettings{
			Profile:                        ProfileTesting,
			DenyPrivilegedByDefault:        true,
			RequireCapabilityForPrivileged: false,
			AuditEnabled:                   true,
			AuditFailClosed:                true,
			AntiReplayEnabled:              true,
			AntiReplayMaxEntries:           512,
			CapabilityMaxTTL:               5 * time.Minute,
			CapabilityDefaultTTL:           time.Minute,
			RequireSingleUseCapabilities:   true,
			LockdownOnAuditTamper:          true,
			LockdownOnSplitBrain:           true,
			LockdownOnCommandFlood:         true,
			CommandFloodThreshold:          20,
			CommandFloodWindow:             time.Minute,
			AllowExec:                      false,
			QuarantineOnTamper:             true,
			MaxCommandBodyBytes:            64 << 10,
			DefaultCommandDeadline:         5 * time.Second,
		}
	case ProfileHardened:
		return SecuritySettings{
			Profile:                        ProfileHardened,
			DenyPrivilegedByDefault:        true,
			RequireCapabilityForPrivileged: true,
			AuditEnabled:                   true,
			AuditFailClosed:                true,
			AntiReplayEnabled:              true,
			AntiReplayMaxEntries:           4096,
			CapabilityMaxTTL:               10 * time.Minute,
			CapabilityDefaultTTL:           2 * time.Minute,
			RequireSingleUseCapabilities:   true,
			LockdownOnAuditTamper:          true,
			LockdownOnSplitBrain:           true,
			LockdownOnCommandFlood:         true,
			CommandFloodThreshold:          10,
			CommandFloodWindow:             time.Minute,
			AllowExec:                      false,
			QuarantineOnTamper:             true,
			MaxCommandBodyBytes:            32 << 10,
			DefaultCommandDeadline:         10 * time.Second,
		}
	case ProfileMaximumSecurity:
		return SecuritySettings{
			Profile:                        ProfileMaximumSecurity,
			DenyPrivilegedByDefault:        true,
			RequireCapabilityForPrivileged: true,
			AuditEnabled:                   true,
			AuditFailClosed:                true,
			AntiReplayEnabled:              true,
			AntiReplayMaxEntries:           8192,
			CapabilityMaxTTL:               2 * time.Minute,
			CapabilityDefaultTTL:           30 * time.Second,
			RequireSingleUseCapabilities:   true,
			LockdownOnAuditTamper:          true,
			LockdownOnSplitBrain:           true,
			LockdownOnCommandFlood:         true,
			CommandFloodThreshold:          5,
			CommandFloodWindow:             time.Minute,
			AllowExec:                      false,
			QuarantineOnTamper:             true,
			MaxCommandBodyBytes:            16 << 10,
			DefaultCommandDeadline:         5 * time.Second,
		}
	case ProfileStandard, "":
		fallthrough
	default:
		if p != ProfileStandard && p != "" {
			// Unknown profiles map to standard (inspectable) rather than silent max.
			_ = fmt.Sprintf("unknown profile %q; using standard", p)
		}
		return SecuritySettings{
			Profile:                        ProfileStandard,
			DenyPrivilegedByDefault:        true,
			RequireCapabilityForPrivileged: false,
			AuditEnabled:                   true,
			AuditFailClosed:                false,
			AntiReplayEnabled:              true,
			AntiReplayMaxEntries:           2048,
			CapabilityMaxTTL:               30 * time.Minute,
			CapabilityDefaultTTL:           5 * time.Minute,
			RequireSingleUseCapabilities:   false,
			LockdownOnAuditTamper:          true,
			LockdownOnSplitBrain:           true,
			LockdownOnCommandFlood:         true,
			CommandFloodThreshold:          30,
			CommandFloodWindow:             time.Minute,
			AllowExec:                      false,
			QuarantineOnTamper:             true,
			MaxCommandBodyBytes:            256 << 10,
			DefaultCommandDeadline:         15 * time.Second,
		}
	}
}

// MergeSecurity overlays non-zero overrides onto base (booleans use pointer-less
// explicit OverrideSecurity for RuntimeConfig).
func MergeSecurity(base SecuritySettings, overlay SecuritySettings) SecuritySettings {
	out := base
	if overlay.Profile != "" {
		out.Profile = overlay.Profile
	}
	// Booleans: overlay wins when OverlaySet is used; here Runtime applies Expand then overlay selectively.
	if overlay.AntiReplayMaxEntries > 0 {
		out.AntiReplayMaxEntries = overlay.AntiReplayMaxEntries
	}
	if overlay.CapabilityMaxTTL > 0 {
		out.CapabilityMaxTTL = overlay.CapabilityMaxTTL
	}
	if overlay.CapabilityDefaultTTL > 0 {
		out.CapabilityDefaultTTL = overlay.CapabilityDefaultTTL
	}
	if overlay.CommandFloodThreshold > 0 {
		out.CommandFloodThreshold = overlay.CommandFloodThreshold
	}
	if overlay.CommandFloodWindow > 0 {
		out.CommandFloodWindow = overlay.CommandFloodWindow
	}
	if overlay.MaxCommandBodyBytes > 0 {
		out.MaxCommandBodyBytes = overlay.MaxCommandBodyBytes
	}
	if overlay.DefaultCommandDeadline > 0 {
		out.DefaultCommandDeadline = overlay.DefaultCommandDeadline
	}
	return out
}

// IsPrivileged reports whether p requires explicit authorization.
func IsPrivileged(p Permission) bool {
	switch p {
	case PermClaimForceRelease, PermMaintenanceEnter, PermMaintenanceExit,
		PermLockdownEnter, PermLockdownExit, PermCommandRegister, PermCapabilityIssue,
		PermCapabilityRevoke, PermExecRun, PermRecoveryAct:
		return true
	default:
		return false
	}
}
