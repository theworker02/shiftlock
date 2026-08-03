package journal

import (
	"context"
	"time"

	"github.com/theworker02/shiftlock"
)

// Entry is a sanitized ownership history record (no secrets).
type Entry struct {
	Seq        uint64                   `json:"seq"`
	Time       time.Time                `json:"time"`
	Service    string                   `json:"service"`
	Claim      string                   `json:"claim,omitempty"`
	Generation string                   `json:"generation,omitempty"`
	Type       string                   `json:"type"`
	Token      shiftlock.FencingToken   `json:"fencing_token,omitempty"`
	Reason     shiftlock.TransitionReason `json:"reason,omitempty"`
	OpID       shiftlock.OperationID    `json:"operation_id,omitempty"`
	Attrs      map[string]string        `json:"attrs,omitempty"`
}

// Journal is an append-only ownership event log.
type Journal interface {
	Append(ctx context.Context, e Entry) error
	Read(ctx context.Context, fromSeq uint64, limit int) ([]Entry, error)
	Export(ctx context.Context, fromSeq uint64) ([]Entry, error)
	Close() error
}

// Observer adapts a Journal to shiftlock.Observer.
type Observer struct {
	J       Journal
	Service string
	seq     uint64
}

func (o *Observer) OnEvent(e shiftlock.Event) {
	if o.J == nil {
		return
	}
	o.seq++
	attrs := map[string]string{}
	for k, v := range e.Attrs {
		// sanitize: drop keys that look like secrets
		if k == "password" || k == "token" || k == "authorization" {
			continue
		}
		attrs[k] = v
	}
	_ = o.J.Append(context.Background(), Entry{
		Seq: o.seq, Time: e.Time, Service: o.Service, Claim: e.Claim,
		Generation: e.Generation, Type: string(e.Type), Token: e.Token,
		Reason: e.Reason, Attrs: attrs,
	})
}

var _ shiftlock.Observer = (*Observer)(nil)
