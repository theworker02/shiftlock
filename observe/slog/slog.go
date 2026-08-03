package slogobs

import (
	"log/slog"

	"github.com/theworker02/shiftlock"
)

// Observer logs ShiftLock events via slog.
type Observer struct {
	Logger *slog.Logger
}

// OnEvent implements shiftlock.Observer.
func (o Observer) OnEvent(e shiftlock.Event) {
	l := o.Logger
	if l == nil {
		l = slog.Default()
	}
	attrs := []any{
		"type", string(e.Type),
		"service", e.Service,
		"generation", e.Generation,
		"claim", e.Claim,
		"token", uint64(e.Token),
		"reason", string(e.Reason),
	}
	if e.Err != "" {
		attrs = append(attrs, "error", e.Err)
	}
	switch e.Type {
	case shiftlock.EventError, shiftlock.EventHandoffFailed, shiftlock.EventClaimLost:
		l.Warn("shiftlock", attrs...)
	default:
		l.Info("shiftlock", attrs...)
	}
}

var _ shiftlock.Observer = Observer{}
