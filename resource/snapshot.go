package resource

import (
	"context"
	"strings"
	"time"
)

// secretKeyHints are never included in sanitized snapshots.
var secretKeyHints = []string{
	"password", "passwd", "secret", "token", "authorization", "api_key", "apikey",
	"credential", "private_key", "dsn", "connection_string",
}

// SnapshotProvider is implemented by adapters that contribute sanitized snapshots.
type SnapshotProvider interface {
	Snapshot(ctx context.Context) (map[string]string, error)
}

// ResourceSnapshot is a sanitized point-in-time view.
type ResourceSnapshot struct {
	ID         ResourceID        `json:"id"`
	Epoch      ResourceEpoch     `json:"epoch"`
	Health     HealthStatus      `json:"health,omitempty"`
	CapturedAt time.Time         `json:"captured_at"`
	Fields     map[string]string `json:"fields,omitempty"`
}

// CaptureSnapshot builds a sanitized snapshot from a registry entry.
func CaptureSnapshot(ctx context.Context, ent *Entry) (ResourceSnapshot, error) {
	if ent == nil || ent.Resource == nil {
		return ResourceSnapshot{}, &Error{Op: "CaptureSnapshot", Err: ErrInvalidArgument, Message: "nil entry"}
	}
	snap := ResourceSnapshot{
		ID:         ent.Resource.ID(),
		Epoch:      ent.Epoch,
		CapturedAt: time.Now().UTC(),
		Fields:     map[string]string{},
	}
	h := ent.Resource.Health(ctx)
	snap.Health = h.Overall
	if sp, ok := ent.Resource.(SnapshotProvider); ok {
		fields, err := sp.Snapshot(ctx)
		if err != nil {
			return snap, err
		}
		snap.Fields = SanitizeSnapshotFields(fields)
	}
	snap.Fields["kind"] = string(ent.Resource.Kind())
	return snap, nil
}

// SanitizeSnapshotFields drops secret-like keys and truncates values.
func SanitizeSnapshotFields(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if isSecretKey(k) {
			continue
		}
		out[truncateRunes(k, MaxEvidenceAttrKey)] = truncateRunes(v, MaxEvidenceAttrValue)
	}
	return out
}

func isSecretKey(k string) bool {
	lk := strings.ToLower(k)
	for _, h := range secretKeyHints {
		if strings.Contains(lk, h) {
			return true
		}
	}
	return false
}
