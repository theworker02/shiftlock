package resource

import (
	"time"
	"unicode/utf8"
)

// Evidence size limits keep audit/checkpoint payloads bounded.
const (
	MaxEvidenceAttrs      = 32
	MaxEvidenceAttrKey    = 64
	MaxEvidenceAttrValue  = 512
	MaxEvidenceSummary    = 1024
	MaxEvidenceBlobBytes  = 4096
)

// Evidence is a sanitized, size-bounded record of an observation or mutation.
// It intentionally mirrors control/lockdown Evidence shape (time/event/attrs)
// while adding resource-fabric fields. Secrets must never be placed in Attrs.
type Evidence struct {
	Time      time.Time         `json:"time"`
	Event     string            `json:"event"`
	ActorID   string            `json:"actor_id,omitempty"`
	Resource  string            `json:"resource,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
	BlobBytes int               `json:"blob_bytes,omitempty"` // size of omitted blob; blob itself not stored here
}

// SanitizeEvidence truncates/drops oversized fields. Returns ErrEvidenceTooLarge
// only when Attrs count exceeds MaxEvidenceAttrs after truncation is impossible.
func SanitizeEvidence(e Evidence) (Evidence, error) {
	out := e
	if out.Time.IsZero() {
		out.Time = time.Now().UTC()
	} else {
		out.Time = out.Time.UTC()
	}
	out.Event = truncateRunes(out.Event, MaxEvidenceAttrKey)
	out.ActorID = truncateRunes(out.ActorID, MaxEvidenceAttrKey)
	out.Resource = truncateRunes(out.Resource, MaxEvidenceSummary)
	out.Summary = truncateRunes(out.Summary, MaxEvidenceSummary)
	if out.BlobBytes > MaxEvidenceBlobBytes {
		out.BlobBytes = MaxEvidenceBlobBytes
	}
	if out.Attrs == nil {
		return out, nil
	}
	if len(out.Attrs) > MaxEvidenceAttrs {
		return Evidence{}, &Error{Op: "SanitizeEvidence", Err: ErrEvidenceTooLarge, Message: "too many attrs"}
	}
	clean := make(map[string]string, len(out.Attrs))
	for k, v := range out.Attrs {
		k = truncateRunes(k, MaxEvidenceAttrKey)
		v = truncateRunes(v, MaxEvidenceAttrValue)
		if k == "" {
			continue
		}
		clean[k] = v
	}
	out.Attrs = clean
	return out, nil
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
