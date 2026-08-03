package resource

// ResourceEpoch is a monotonic generation for a registered resource.
// It must never decrease or wrap silently; overflow is a terminal error.
type ResourceEpoch uint64

// MaxResourceEpoch is the last valid epoch before terminal overflow.
const MaxResourceEpoch ResourceEpoch = ResourceEpoch(^uint64(0) - 1)

// Next returns epoch+1 or ErrEpochOverflow.
func (e ResourceEpoch) Next() (ResourceEpoch, error) {
	if e >= MaxResourceEpoch {
		return e, &Error{Op: "ResourceEpoch.Next", Err: ErrEpochOverflow, Message: "resource epoch overflow"}
	}
	return e + 1, nil
}

// EpochAdvance records why an epoch advanced.
type EpochAdvance struct {
	From   ResourceEpoch `json:"from"`
	To     ResourceEpoch `json:"to"`
	Reason string        `json:"reason"`
}

// AdvanceEpoch increments e when reason is non-empty.
func AdvanceEpoch(e ResourceEpoch, reason string) (ResourceEpoch, EpochAdvance, error) {
	if reason == "" {
		return e, EpochAdvance{}, &Error{Op: "AdvanceEpoch", Err: ErrInvalidArgument, Message: "reason required"}
	}
	next, err := e.Next()
	if err != nil {
		return e, EpochAdvance{}, err
	}
	return next, EpochAdvance{From: e, To: next, Reason: reason}, nil
}

// EnsureNotDecreased returns ErrEpochDecreased if proposed < current.
func EnsureNotDecreased(current, proposed ResourceEpoch) error {
	if proposed < current {
		return &Error{Op: "EnsureNotDecreased", Err: ErrEpochDecreased, Message: "proposed epoch is older"}
	}
	return nil
}
