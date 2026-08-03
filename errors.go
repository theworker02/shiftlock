package shiftlock

import (
	"errors"
	"fmt"
)

// ErrorCategory groups errors for operators and stable API docs.
type ErrorCategory string

const (
	CategoryGeneral       ErrorCategory = "general"
	CategorySecurity      ErrorCategory = "security"
	CategoryAuthorization ErrorCategory = "authorization"
	CategoryLockdown      ErrorCategory = "lockdown"
	CategoryMaintenance   ErrorCategory = "maintenance"
	CategoryCapability    ErrorCategory = "capability"
	CategoryAudit         ErrorCategory = "audit"
	CategoryQuarantine    ErrorCategory = "quarantine"
	CategoryPolicy        ErrorCategory = "policy"
	CategoryBackend       ErrorCategory = "backend"
	CategoryOwnership     ErrorCategory = "ownership"
)

// Stable public error codes (safe to expose; no secrets).
const (
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeReplay           = "replay"
	CodeCapabilityInvalid = "capability_invalid"
	CodeCapabilityExpired = "capability_expired"
	CodeCapabilityRevoked = "capability_revoked"
	CodeEpochOverflow    = "epoch_overflow"
	CodeAuditTamper      = "audit_tamper"
	CodeLockdownActive   = "lockdown_active"
	CodeMaintenanceActive = "maintenance_active"
	CodeQuarantined      = "quarantined"
	CodeGuardDenied      = "guard_denied"
	CodeInvalidArgument  = "invalid_argument"
	CodeDeadlineExceeded = "deadline_exceeded"
	CodeRateLimited      = "rate_limited"
	CodeExecDenied       = "exec_denied"
)

// Sentinel errors for stable caller matching via errors.Is.
var (
	ErrClosed              = errors.New("shiftlock: coordinator closed")
	ErrNotOwner            = errors.New("shiftlock: not claim owner")
	ErrAlreadyOwner        = errors.New("shiftlock: already claim owner")
	ErrClaimHeld           = errors.New("shiftlock: claim held by another generation")
	ErrStaleToken          = errors.New("shiftlock: stale fencing token")
	ErrInvalidState        = errors.New("shiftlock: invalid state transition")
	ErrDraining            = errors.New("shiftlock: draining in progress")
	ErrTransferPending     = errors.New("shiftlock: transfer already pending")
	ErrNoTransfer          = errors.New("shiftlock: no pending transfer")
	ErrTransferFailed      = errors.New("shiftlock: transfer failed")
	ErrHandoffAborted      = errors.New("shiftlock: handoff aborted")
	ErrTimeout             = errors.New("shiftlock: operation timed out")
	ErrNotReady            = errors.New("shiftlock: readiness gates not satisfied")
	ErrPolicy              = errors.New("shiftlock: policy validation failed")
	ErrBackend             = errors.New("shiftlock: backend error")
	ErrGenerationRetired   = errors.New("shiftlock: generation retired")
	ErrGenerationFailed    = errors.New("shiftlock: generation failed")
	ErrClaimNotFound       = errors.New("shiftlock: claim not found")
	ErrGenerationNotFound  = errors.New("shiftlock: generation not found")
	ErrConcurrentTransfer  = errors.New("shiftlock: concurrent transfer conflict")
	ErrLeaseLost           = errors.New("shiftlock: lease lost")
	ErrCanceled            = errors.New("shiftlock: canceled")
	ErrTokenOverflow       = errors.New("shiftlock: fencing token overflow")
	ErrAmbiguous           = errors.New("shiftlock: ambiguous backend outcome")
	ErrClaimUnavailable    = errors.New("shiftlock: claim unavailable")
	ErrCapability          = errors.New("shiftlock: backend capability mismatch")
	ErrSplitBrain          = errors.New("shiftlock: split-brain detected")

	// Phase 6 security / control-plane sentinels.
	ErrUnauthorized           = errors.New("shiftlock: unauthorized")
	ErrForbidden              = errors.New("shiftlock: forbidden")
	ErrReplay                 = errors.New("shiftlock: replay detected")
	ErrCapabilityToken        = errors.New("shiftlock: capability token invalid")
	ErrSecurityEpochOverflow  = errors.New("shiftlock: security epoch overflow")
	ErrAuditTamper            = errors.New("shiftlock: audit chain tamper detected")
	ErrLockdown               = errors.New("shiftlock: lockdown active")
	ErrMaintenance            = errors.New("shiftlock: maintenance active")
	ErrQuarantined            = errors.New("shiftlock: generation quarantined")
	ErrGuardDenied            = errors.New("shiftlock: guard denied")
	ErrExecDenied             = errors.New("shiftlock: exec denied")
	ErrRateLimited            = errors.New("shiftlock: rate limited")
	ErrRuntimeClosed          = errors.New("shiftlock: runtime closed")
)

// Error is a typed shiftlock error with optional cause and context.
type Error struct {
	Op       string
	Claim    string
	Gen      string
	Token    FencingToken
	Reason   TransitionReason
	Err      error
	Message  string
	Code     string
	Category ErrorCategory
}

func (e *Error) Error() string {
	if e == nil {
		return "shiftlock: <nil>"
	}
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		msg = "error"
	}
	switch {
	case e.Op != "" && e.Claim != "":
		return fmt.Sprintf("shiftlock: %s claim=%q: %s", e.Op, e.Claim, msg)
	case e.Op != "":
		return fmt.Sprintf("shiftlock: %s: %s", e.Op, msg)
	default:
		return fmt.Sprintf("shiftlock: %s", msg)
	}
}

// PublicMessage returns a caller-safe message (no internal paths/secrets).
func (e *Error) PublicMessage() string {
	if e == nil {
		return "error"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "error"
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	return errors.Is(e.Err, target)
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	var se *Error
	if errors.As(err, &se) {
		if se.Op == "" {
			se.Op = op
		}
		return se
	}
	return &Error{Op: op, Err: err, Message: err.Error()}
}

func wrapClaim(op, claim string, err error) error {
	if err == nil {
		return nil
	}
	var se *Error
	if errors.As(err, &se) {
		if se.Op == "" {
			se.Op = op
		}
		if se.Claim == "" {
			se.Claim = claim
		}
		return se
	}
	return &Error{Op: op, Claim: claim, Err: err, Message: err.Error()}
}
