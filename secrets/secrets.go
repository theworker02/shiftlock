// Package secrets provides opaque secret references and redaction helpers.
//
// ShiftLock never becomes a secret manager: values are resolved at use sites
// and must not appear in logs, diagnostics, or incident bundles.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

var (
	ErrUnsupportedScheme = errors.New("secrets: unsupported scheme")
	ErrEmptyRef          = errors.New("secrets: empty reference")
	ErrResolveFailed     = errors.New("secrets: resolve failed")
)

// Ref is an opaque secret locator. String() never includes resolved material.
type Ref struct {
	raw string
}

// ParseRef accepts env://NAME or file://path (and file:///absolute).
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, ErrEmptyRef
	}
	switch {
	case strings.HasPrefix(s, "env://"):
		if strings.TrimPrefix(s, "env://") == "" {
			return Ref{}, fmt.Errorf("%w: missing env name", ErrEmptyRef)
		}
	case strings.HasPrefix(s, "file://"):
		if strings.TrimPrefix(s, "file://") == "" {
			return Ref{}, fmt.Errorf("%w: missing file path", ErrEmptyRef)
		}
	default:
		return Ref{}, fmt.Errorf("%w: %q", ErrUnsupportedScheme, schemeOf(s))
	}
	return Ref{raw: s}, nil
}

func schemeOf(s string) string {
	i := strings.Index(s, "://")
	if i < 0 {
		return ""
	}
	return s[:i]
}

// String returns the opaque reference (never the secret).
func (r Ref) String() string { return r.raw }

// Scheme returns env or file.
func (r Ref) Scheme() string { return schemeOf(r.raw) }

// PathOrName returns the env var name or filesystem path.
func (r Ref) PathOrName() string {
	switch r.Scheme() {
	case "env":
		return strings.TrimPrefix(r.raw, "env://")
	case "file":
		p := strings.TrimPrefix(r.raw, "file://")
		// file:///etc/secret → /etc/secret on Unix; file://C:/x on Windows stays as C:/x after trim of one slash handling:
		if strings.HasPrefix(p, "/") && len(p) > 2 && p[2] == ':' {
			// file:///C:/path → /C:/path → C:/path
			return p[1:]
		}
		return p
	default:
		return ""
	}
}

// Value is a resolved secret. Do not log, fmt, or JSON-encode it.
type Value struct {
	data []byte
}

// Bytes returns a copy of the secret bytes.
func (v Value) Bytes() []byte {
	if v.data == nil {
		return nil
	}
	out := make([]byte, len(v.data))
	copy(out, v.data)
	return out
}

// String is intentionally unhelpful to reduce accidental logging.
func (v Value) String() string { return "[redacted]" }

// GoString for %#v.
func (v Value) GoString() string { return "secrets.Value{/*redacted*/}" }

// Clear overwrites the backing buffer.
func (v *Value) Clear() {
	for i := range v.data {
		v.data[i] = 0
	}
	v.data = nil
}

// Resolver resolves opaque refs. Implementations must not log values.
type Resolver interface {
	Resolve(ref Ref) (Value, error)
}

// EnvFileResolver resolves env:// and file:// references.
type EnvFileResolver struct {
	ReadFile func(name string) ([]byte, error)
	LookupEnv func(key string) (string, bool)
}

// Resolve implements Resolver.
func (r EnvFileResolver) Resolve(ref Ref) (Value, error) {
	readFile := r.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	lookup := r.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	switch ref.Scheme() {
	case "env":
		name := ref.PathOrName()
		v, ok := lookup(name)
		if !ok {
			return Value{}, fmt.Errorf("%w: env %q not set", ErrResolveFailed, name)
		}
		return Value{data: []byte(v)}, nil
	case "file":
		b, err := readFile(ref.PathOrName())
		if err != nil {
			return Value{}, fmt.Errorf("%w: %v", ErrResolveFailed, err)
		}
		return Value{data: b}, nil
	default:
		return Value{}, ErrUnsupportedScheme
	}
}

var (
	redactMu sync.Mutex
	patterns = []*regexp.Regexp{
		// Bearer tokens before key=value so "Authorization: Bearer …" is fully consumed.
		regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-._~+/]+=*`),
		regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|authorization|private[_-]?key)\s*[=:]\s*\S+`),
		regexp.MustCompile(`env://\S+`),
		regexp.MustCompile(`file://\S+`),
	}
)

// Redact replaces likely secret material in s with [REDACTED].
func Redact(s string) string {
	redactMu.Lock()
	defer redactMu.Unlock()
	out := s
	for _, re := range patterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			if strings.HasPrefix(strings.ToLower(m), "bearer ") {
				return "Bearer [REDACTED]"
			}
			if i := strings.IndexAny(m, "=:"); i >= 0 {
				return m[:i+1] + " [REDACTED]"
			}
			if strings.Contains(m, "://") {
				return schemeOf(m) + "://[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return out
}

// RedactMap returns a copy with sensitive-looking keys/values redacted.
func RedactMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "secret") || strings.Contains(lk, "password") ||
			strings.Contains(lk, "token") || strings.Contains(lk, "credential") ||
			strings.HasSuffix(lk, "_key") || strings.Contains(lk, "authorization") {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = Redact(v)
	}
	return out
}
