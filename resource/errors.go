package resource

import (
	"errors"
	"fmt"
)

// Sentinel errors for stable matching via errors.Is.
var (
	ErrInvalidID           = errors.New("resource: invalid id")
	ErrUnknownKind         = errors.New("resource: unknown kind")
	ErrDuplicate           = errors.New("resource: duplicate registration")
	ErrNotFound            = errors.New("resource: not found")
	ErrBoundExceeded       = errors.New("resource: bound exceeded")
	ErrCycle               = errors.New("resource: dependency cycle")
	ErrCapabilityClaimed   = errors.New("resource: capability not supported")
	ErrEpochOverflow       = errors.New("resource: epoch overflow")
	ErrEpochDecreased      = errors.New("resource: epoch must not decrease")
	ErrLockdown            = errors.New("resource: lockdown blocks mutation")
	ErrClosed              = errors.New("resource: registry closed")
	ErrBundleNotReady      = errors.New("resource: bundle not ready")
	ErrDependencyBlocked   = errors.New("resource: dependency blocked")
	ErrPartialAcquire      = errors.New("resource: partial acquire released")
	ErrEvidenceTooLarge    = errors.New("resource: evidence exceeds size limit")
	ErrInvalidArgument     = errors.New("resource: invalid argument")
)

// Error is a typed resource error with optional context.
type Error struct {
	Op      string
	ID      ResourceID
	Err     error
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "resource: <nil>"
	}
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		msg = "error"
	}
	switch {
	case e.Op != "" && e.ID.Name != "":
		return fmt.Sprintf("resource: %s id=%q: %s", e.Op, e.ID.String(), msg)
	case e.Op != "":
		return fmt.Sprintf("resource: %s: %s", e.Op, msg)
	default:
		return "resource: " + msg
	}
}

func (e *Error) Unwrap() error { return e.Err }

func wrap(op string, id ResourceID, err error, msg string) error {
	if err == nil {
		return nil
	}
	return &Error{Op: op, ID: id, Err: err, Message: msg}
}
