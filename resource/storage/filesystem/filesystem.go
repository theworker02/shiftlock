// Package filesystem provides a hardened directory resource adapter.
//
// Features: exclusive dir ownership helpers, path traversal rejection,
// atomic replace, and checksums. Unsafe relative paths are rejected.
package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/resource"
)

// Config configures a directory resource.
type Config struct {
	ID          resource.ResourceID
	Path        string // absolute directory root
	DisplayName string
	// Hardened rejects ".." and absolute escapes under Path.
	Hardened bool
}

// Resource implements resource.Resource for a local directory.
type Resource struct {
	cfg   Config
	mu    sync.Mutex
	owner string // exclusive owner identity; empty = unlocked
}

// New validates path rules and constructs the resource.
func New(cfg Config) (*Resource, error) {
	if cfg.Path == "" {
		return nil, errors.New("filesystem: Path required")
	}
	if cfg.ID.Kind == "" {
		cfg.ID.Kind = resource.KindFilesystem
	}
	if cfg.ID.Kind != resource.KindFilesystem {
		return nil, errors.New("filesystem: id kind must be filesystem")
	}
	if err := cfg.ID.Validate(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(cfg.Path)
	if err != nil {
		return nil, err
	}
	if cfg.Hardened {
		if !filepath.IsAbs(abs) {
			return nil, errors.New("filesystem: hardened mode requires absolute path")
		}
		clean := filepath.Clean(abs)
		if clean != abs && containsDotDot(cfg.Path) {
			return nil, errors.New("filesystem: path contains '..'")
		}
		cfg.Path = clean
	} else {
		cfg.Path = abs
	}
	return &Resource{cfg: cfg}, nil
}

func containsDotDot(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func (r *Resource) ID() resource.ResourceID { return r.cfg.ID }
func (r *Resource) Kind() resource.Kind     { return resource.KindFilesystem }

func (r *Resource) Describe() resource.Description {
	name := r.cfg.DisplayName
	if name == "" {
		name = r.cfg.ID.Name
	}
	return resource.Description{
		DisplayName: name,
		Summary:     "Filesystem directory resource",
		Labels:      map[string]string{"adapter": "filesystem"},
	}
}

func (r *Resource) Capabilities() resource.ResourceCapabilities {
	return resource.ResourceCapabilities{
		SupportsOwnership: true, // exclusive directory ownership helper
		SupportsHealth:    true,
		SupportsSnapshots: true,
		SupportsRecovery:  true,
		// File locks here are cooperative process-local; not distributed fencing.
		SupportsFencing: false,
	}
}

func (r *Resource) Health(ctx context.Context) resource.ResourceHealth {
	_ = ctx
	h := resource.ResourceHealth{
		CheckedAt:  time.Now().UTC(),
		Dimensions: map[resource.HealthDimension]resource.DimensionHealth{},
	}
	fi, err := os.Stat(r.cfg.Path)
	if err != nil {
		if os.IsNotExist(err) {
			h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
				Status: resource.HealthUnhealthy, Message: "directory missing",
			}
		} else {
			h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{
				Status: resource.HealthUnhealthy, Message: err.Error(),
			}
		}
		h.ComputeOverall()
		return h
	}
	if !fi.IsDir() {
		h.Dimensions[resource.DimConfiguration] = resource.DimensionHealth{
			Status: resource.HealthUnhealthy, Message: "path is not a directory",
		}
		h.ComputeOverall()
		return h
	}
	h.Dimensions[resource.DimAvailability] = resource.DimensionHealth{Status: resource.HealthHealthy}
	h.Dimensions[resource.DimDurability] = resource.DimensionHealth{Status: resource.HealthHealthy}
	r.mu.Lock()
	owner := r.owner
	r.mu.Unlock()
	if owner != "" {
		h.Dimensions[resource.DimAuthz] = resource.DimensionHealth{
			Status: resource.HealthHealthy, Message: "owned",
		}
	}
	h.ComputeOverall()
	h.Message = "ok"
	return h
}

// Root returns the absolute root path.
func (r *Resource) Root() string { return r.cfg.Path }

// Resolve joins a relative path under Root and rejects traversal.
func (r *Resource) Resolve(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("filesystem: empty relative path")
	}
	// Reject absolute and root-anchored paths on all platforms (filepath.IsAbs
	// does not treat "/etc/passwd" as absolute on Windows).
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") ||
		(len(rel) >= 2 && rel[1] == ':') {
		return "", errors.New("filesystem: absolute paths rejected")
	}
	if containsDotDot(rel) {
		return "", errors.New("filesystem: path traversal rejected")
	}
	joined := filepath.Join(r.cfg.Path, rel)
	clean := filepath.Clean(joined)
	relToRoot, err := filepath.Rel(r.cfg.Path, clean)
	if err != nil {
		return "", err
	}
	if relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("filesystem: path escapes root")
	}
	return clean, nil
}

// AcquireExclusive claims exclusive directory ownership for ownerID.
func (r *Resource) AcquireExclusive(ownerID string) error {
	if ownerID == "" {
		return errors.New("filesystem: ownerID required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner != "" && r.owner != ownerID {
		return fmt.Errorf("filesystem: directory owned by %q", r.owner)
	}
	r.owner = ownerID
	return nil
}

// ReleaseExclusive releases ownership if held by ownerID.
func (r *Resource) ReleaseExclusive(ownerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == "" {
		return nil
	}
	if r.owner != ownerID {
		return errors.New("filesystem: not owner")
	}
	r.owner = ""
	return nil
}

// Owner returns the current exclusive owner, if any.
func (r *Resource) Owner() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.owner
}

// EnsureDir creates the root directory if missing.
func (r *Resource) EnsureDir() error {
	return os.MkdirAll(r.cfg.Path, 0o750)
}

// AtomicReplace writes data to a temp file then renames over dest (relative).
func (r *Resource) AtomicReplace(rel string, data []byte) error {
	dest, err := r.Resolve(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".shiftlock-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// ChecksumSHA256 returns the hex SHA-256 of a file under the root.
func (r *Resource) ChecksumSHA256(rel string) (string, error) {
	path, err := r.Resolve(rel)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Snapshot returns sanitized metadata (paths only as root basename; no file contents).
func (r *Resource) Snapshot(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	owner := r.owner
	r.mu.Unlock()
	out := map[string]string{
		"adapter": "filesystem",
		"root":    filepath.Base(r.cfg.Path),
	}
	if owner != "" {
		out["owner"] = owner
	}
	if fi, err := os.Stat(r.cfg.Path); err == nil {
		out["exists"] = "true"
		out["isdir"] = fmt.Sprintf("%v", fi.IsDir())
	} else {
		out["exists"] = "false"
	}
	return out, nil
}
