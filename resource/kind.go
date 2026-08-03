package resource

import "sync"

// Kind classifies a resource in the fabric.
type Kind string

const (
	KindDatabase         Kind = "database"
	KindQueue            Kind = "queue"
	KindStream           Kind = "stream"
	KindCache            Kind = "cache"
	KindFilesystem       Kind = "filesystem"
	KindObjectStore      Kind = "object-store"
	KindHTTPService      Kind = "http-service"
	KindGRPCService      Kind = "grpc-service"
	KindWorker           Kind = "worker"
	KindScheduler         Kind = "scheduler"
	KindDeployment       Kind = "deployment"
	KindConfiguration    Kind = "configuration"
	KindSecretReference  Kind = "secret-reference"
	KindRateLimit        Kind = "rate-limit"
	KindFeature          Kind = "feature"
	KindCustom           Kind = "custom"
)

var (
	builtinKinds = map[Kind]struct{}{
		KindDatabase: {}, KindQueue: {}, KindStream: {}, KindCache: {},
		KindFilesystem: {}, KindObjectStore: {}, KindHTTPService: {}, KindGRPCService: {},
		KindWorker: {}, KindScheduler: {}, KindDeployment: {}, KindConfiguration: {},
		KindSecretReference: {}, KindRateLimit: {}, KindFeature: {}, KindCustom: {},
	}
	customMu    sync.RWMutex
	customKinds = map[Kind]struct{}{}
)

// ValidKind reports whether k is a built-in or registered custom kind.
func ValidKind(k Kind) bool {
	if _, ok := builtinKinds[k]; ok {
		return true
	}
	customMu.RLock()
	defer customMu.RUnlock()
	_, ok := customKinds[k]
	return ok
}

// RegisterCustomKind registers an additional kind name (not a global resource
// registry — only kind vocabulary). Empty or slash-containing names are rejected.
// Built-in kinds cannot be re-registered.
func RegisterCustomKind(k Kind) error {
	if k == "" || len(string(k)) > 64 {
		return &Error{Op: "RegisterCustomKind", Err: ErrInvalidArgument, Message: "invalid kind name"}
	}
	for _, r := range string(k) {
		if r == '/' || r == ' ' || r < 0x20 {
			return &Error{Op: "RegisterCustomKind", Err: ErrInvalidArgument, Message: "invalid kind characters"}
		}
	}
	if _, ok := builtinKinds[k]; ok {
		return &Error{Op: "RegisterCustomKind", Err: ErrDuplicate, Message: "built-in kind"}
	}
	customMu.Lock()
	defer customMu.Unlock()
	if _, ok := customKinds[k]; ok {
		return &Error{Op: "RegisterCustomKind", Err: ErrDuplicate, Message: "already registered"}
	}
	customKinds[k] = struct{}{}
	return nil
}

// KnownKinds returns built-in plus registered custom kinds (unsorted).
func KnownKinds() []Kind {
	out := make([]Kind, 0, len(builtinKinds)+8)
	for k := range builtinKinds {
		out = append(out, k)
	}
	customMu.RLock()
	defer customMu.RUnlock()
	for k := range customKinds {
		out = append(out, k)
	}
	return out
}
