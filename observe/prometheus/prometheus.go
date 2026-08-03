package prometheus

import (
	"sync/atomic"

	"github.com/theworker02/shiftlock"
)

// Metrics holds simple counters without importing prometheus client libraries.
// Wire these into your own prometheus.CounterVec via Collect callbacks, or use
// the optional Register helpers when github.com/prometheus/client_golang is available.
//
// This package intentionally avoids a hard dependency on client_golang so the
// core module stays lean. Counters are atomic and safe for concurrent use.
type Metrics struct {
	ClaimsAcquired   atomic.Uint64
	ClaimsLost       atomic.Uint64
	TransfersCommit  atomic.Uint64
	TransfersAbort   atomic.Uint64
	HandoffsComplete atomic.Uint64
	HandoffsFailed   atomic.Uint64
	Errors           atomic.Uint64
	EventsTotal      atomic.Uint64
}

// Observer increments Metrics from events.
type Observer struct {
	M *Metrics
}

// OnEvent implements shiftlock.Observer.
func (o Observer) OnEvent(e shiftlock.Event) {
	m := o.M
	if m == nil {
		return
	}
	m.EventsTotal.Add(1)
	switch e.Type {
	case shiftlock.EventClaimAcquired:
		m.ClaimsAcquired.Add(1)
	case shiftlock.EventClaimLost, shiftlock.EventLeaseRevoked:
		m.ClaimsLost.Add(1)
	case shiftlock.EventTransferCommitted:
		m.TransfersCommit.Add(1)
	case shiftlock.EventTransferAborted:
		m.TransfersAbort.Add(1)
	case shiftlock.EventHandoffCompleted:
		m.HandoffsComplete.Add(1)
	case shiftlock.EventHandoffFailed:
		m.HandoffsFailed.Add(1)
	case shiftlock.EventError:
		m.Errors.Add(1)
	}
}

var _ shiftlock.Observer = Observer{}
