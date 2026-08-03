package workflow

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidDefinition     = errors.New("workflow: invalid definition")
	ErrUnknownStep           = errors.New("workflow: unknown step")
	ErrCycle                 = errors.New("workflow: step dependency cycle")
	ErrInvalidState          = errors.New("workflow: invalid state transition")
	ErrNotFound              = errors.New("workflow: instance not found")
	ErrClosed                = errors.New("workflow: engine closed")
	ErrLockdown              = errors.New("workflow: lockdown blocks mutation")
	ErrCapability            = errors.New("workflow: resource capability validation failed")
	ErrAmbiguous             = errors.New("workflow: ambiguous step outcome")
	ErrRequiresReconciliation = errors.New("workflow: requires reconciliation")
	ErrNotRetryable          = errors.New("workflow: step is not retryable")
	ErrCompensationFailed    = errors.New("workflow: compensation failed")
	ErrStaleEpoch            = errors.New("workflow: resource epoch newer than compensation target")
	ErrCancelled             = errors.New("workflow: cancelled")
	ErrBoundExceeded         = errors.New("workflow: bound exceeded")
	ErrInvalidArgument       = errors.New("workflow: invalid argument")
)

// Error is a typed workflow error.
type Error struct {
	Op       string
	Workflow string
	Step     string
	Err      error
	Message  string
}

func (e *Error) Error() string {
	if e == nil {
		return "workflow: <nil>"
	}
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		msg = "error"
	}
	switch {
	case e.Op != "" && e.Step != "":
		return fmt.Sprintf("workflow: %s workflow=%q step=%q: %s", e.Op, e.Workflow, e.Step, msg)
	case e.Op != "" && e.Workflow != "":
		return fmt.Sprintf("workflow: %s workflow=%q: %s", e.Op, e.Workflow, msg)
	case e.Op != "":
		return fmt.Sprintf("workflow: %s: %s", e.Op, msg)
	default:
		return "workflow: " + msg
	}
}

func (e *Error) Unwrap() error { return e.Err }
