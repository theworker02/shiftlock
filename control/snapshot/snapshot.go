// Package snapshot creates sanitized runtime snapshots for inspection and diff.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/theworker02/shiftlock/secrets"
)

// SchemaVersion is the snapshot document schema.
const SchemaVersion = 1

// Snapshot is a sanitized, hashable runtime view with no secrets.
type Snapshot struct {
	SchemaVersion int               `json:"schema_version"`
	CreatedAt     time.Time         `json:"created_at"`
	Service       string            `json:"service,omitempty"`
	InstanceID    string            `json:"instance_id,omitempty"`
	ContentHash   string            `json:"content_hash"`
	Data          map[string]any    `json:"data"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// Create builds a snapshot from arbitrary data, redacting secrets and hashing content.
func Create(service, instance string, data map[string]any, labels map[string]string) (Snapshot, error) {
	cleanAny := sanitize(data)
	clean, _ := cleanAny.(map[string]any)
	if clean == nil {
		clean = map[string]any{}
	}
	cleanLabels := secrets.RedactMap(labels)
	s := Snapshot{
		SchemaVersion: SchemaVersion,
		CreatedAt:     time.Now().UTC(),
		Service:       service,
		InstanceID:    instance,
		Data:          clean,
		Labels:        cleanLabels,
	}
	h, err := contentHash(s)
	if err != nil {
		return Snapshot{}, err
	}
	s.ContentHash = h
	return s, nil
}

func contentHash(s Snapshot) (string, error) {
	body := struct {
		SchemaVersion int               `json:"schema_version"`
		Service       string            `json:"service,omitempty"`
		InstanceID    string            `json:"instance_id,omitempty"`
		Data          map[string]any    `json:"data"`
		Labels        map[string]string `json:"labels,omitempty"`
	}{s.SchemaVersion, s.Service, s.InstanceID, s.Data, s.Labels}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func sanitize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lk := k
			if looksSecretKey(lk) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = sanitize(t[k])
		}
		return out
	case map[string]string:
		return secrets.RedactMap(t)
	case string:
		return secrets.Redact(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = sanitize(t[i])
		}
		return out
	default:
		return t
	}
}

func looksSecretKey(k string) bool {
	lk := ""
	for _, c := range k {
		if c >= 'A' && c <= 'Z' {
			lk += string(c + 32)
		} else {
			lk += string(c)
		}
	}
	for _, p := range []string{"secret", "password", "token", "credential", "private_key", "api_key"} {
		if contains(lk, p) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// DiffEntry describes one changed path.
type DiffEntry struct {
	Path string `json:"path"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
	Op   string `json:"op"` // add|remove|change
}

// Diff compares two snapshots' Data maps.
func Diff(a, b Snapshot) []DiffEntry {
	var out []DiffEntry
	diffMaps("", asMap(a.Data), asMap(b.Data), &out)
	return out
}

func asMap(v map[string]any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	return v
}

func diffMaps(prefix string, a, b map[string]any, out *[]DiffEntry) {
	seen := map[string]struct{}{}
	for k, av := range a {
		seen[k] = struct{}{}
		path := join(prefix, k)
		bv, ok := b[k]
		if !ok {
			*out = append(*out, DiffEntry{Path: path, From: av, Op: "remove"})
			continue
		}
		am, aOK := av.(map[string]any)
		bm, bOK := bv.(map[string]any)
		if aOK && bOK {
			diffMaps(path, am, bm, out)
			continue
		}
		if fmt.Sprintf("%v", av) != fmt.Sprintf("%v", bv) {
			*out = append(*out, DiffEntry{Path: path, From: av, To: bv, Op: "change"})
		}
	}
	for k, bv := range b {
		if _, ok := seen[k]; ok {
			continue
		}
		*out = append(*out, DiffEntry{Path: join(prefix, k), To: bv, Op: "add"})
	}
}

func join(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// Marshal returns indented JSON.
func (s Snapshot) Marshal() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
