package resource

import (
	"fmt"
	"strings"
	"unicode"
)

// ResourceID uniquely names a resource in the fabric.
// Canonical string form: kind/environment/service/name
// Example: database/production/payments-api/orders
type ResourceID struct {
	Kind        Kind   `json:"kind"`
	Environment string `json:"environment"`
	Service     string `json:"service"`
	Name        string `json:"name"`
}

// String returns the canonical slash-separated form.
func (id ResourceID) String() string {
	return string(id.Kind) + "/" + id.Environment + "/" + id.Service + "/" + id.Name
}

// IsZero reports whether all fields are empty.
func (id ResourceID) IsZero() bool {
	return id.Kind == "" && id.Environment == "" && id.Service == "" && id.Name == ""
}

// Equal reports structural equality.
func (id ResourceID) Equal(o ResourceID) bool {
	return id.Kind == o.Kind && id.Environment == o.Environment &&
		id.Service == o.Service && id.Name == o.Name
}

// Validate checks segment rules without requiring the kind to be registered.
func (id ResourceID) Validate() error {
	if id.Kind == "" || id.Environment == "" || id.Service == "" || id.Name == "" {
		return &Error{Op: "ResourceID.Validate", ID: id, Err: ErrInvalidID, Message: "all segments required"}
	}
	for _, seg := range []string{string(id.Kind), id.Environment, id.Service, id.Name} {
		if err := validateSegment(seg); err != nil {
			return &Error{Op: "ResourceID.Validate", ID: id, Err: ErrInvalidID, Message: err.Error()}
		}
	}
	return nil
}

// ParseResourceID parses kind/environment/service/name.
func ParseResourceID(s string) (ResourceID, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, "/")
	if len(parts) != 4 {
		return ResourceID{}, &Error{Op: "ParseResourceID", Err: ErrInvalidID, Message: "want kind/environment/service/name"}
	}
	id := ResourceID{
		Kind:        Kind(parts[0]),
		Environment: parts[1],
		Service:     parts[2],
		Name:        parts[3],
	}
	if err := id.Validate(); err != nil {
		return ResourceID{}, err
	}
	if !ValidKind(id.Kind) {
		return ResourceID{}, &Error{Op: "ParseResourceID", ID: id, Err: ErrUnknownKind, Message: fmt.Sprintf("unknown kind %q", id.Kind)}
	}
	return id, nil
}

// MustParseResourceID panics on parse failure (tests/fixtures only).
func MustParseResourceID(s string) ResourceID {
	id, err := ParseResourceID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func validateSegment(s string) error {
	if s == "" {
		return fmt.Errorf("empty segment")
	}
	if strings.Contains(s, "/") {
		return fmt.Errorf("segment must not contain '/'")
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return fmt.Errorf("segment must not contain whitespace")
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("segment contains control character")
		}
	}
	return nil
}
