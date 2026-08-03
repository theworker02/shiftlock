package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultMaxInstances bounds in-memory/file store cardinality.
const DefaultMaxInstances = 256

// Checkpoint is durable workflow progress.
type Checkpoint struct {
	InstanceID string               `json:"instance_id"`
	Workflow   string               `json:"workflow"`
	State      State                `json:"state"`
	Steps      map[string]StepState `json:"steps"`
	UpdatedAt  time.Time            `json:"updated_at"`
	DryRun     bool                 `json:"dry_run,omitempty"`
	Attrs      map[string]string    `json:"attrs,omitempty"`
	// CompletedSteps lists successfully completed steps in order (for compensate).
	CompletedSteps []string `json:"completed_steps,omitempty"`
	// EpochAtStep records resource epochs observed at step completion.
	EpochAtStep map[string]uint64 `json:"epoch_at_step,omitempty"`
}

// Store persists checkpoints.
type Store interface {
	Save(cp Checkpoint) error
	Load(instanceID string) (Checkpoint, error)
	Delete(instanceID string) error
	List() ([]Checkpoint, error)
}

// MemoryStore is an in-process durable store for tests and local-first mode.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]Checkpoint
	max  int
}

// NewMemoryStore creates a MemoryStore.
func NewMemoryStore(max int) *MemoryStore {
	if max <= 0 {
		max = DefaultMaxInstances
	}
	return &MemoryStore{data: make(map[string]Checkpoint), max: max}
}

func (s *MemoryStore) Save(cp Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[cp.InstanceID]; !ok && len(s.data) >= s.max {
		return &Error{Op: "Store.Save", Err: ErrBoundExceeded, Message: "max instances"}
	}
	cp.UpdatedAt = time.Now().UTC()
	s.data[cp.InstanceID] = cloneCheckpoint(cp)
	return nil
}

func (s *MemoryStore) Load(instanceID string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.data[instanceID]
	if !ok {
		return Checkpoint{}, &Error{Op: "Store.Load", Err: ErrNotFound, Message: "not found"}
	}
	return cloneCheckpoint(cp), nil
}

func (s *MemoryStore) Delete(instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, instanceID)
	return nil
}

func (s *MemoryStore) List() ([]Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Checkpoint, 0, len(s.data))
	for _, cp := range s.data {
		out = append(out, cloneCheckpoint(cp))
	}
	return out, nil
}

// FileStore persists checkpoints as a single JSON map with fsync-ish flush
// (write temp → Sync → rename). Suitable for local-first single-process use.
type FileStore struct {
	path string
	mu   sync.Mutex
	max  int
}

// NewFileStore creates a file-backed store. Parent directories are created on first Save.
func NewFileStore(path string, max int) *FileStore {
	if max <= 0 {
		max = DefaultMaxInstances
	}
	return &FileStore{path: path, max: max}
}

func (s *FileStore) Save(cp Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return err
	}
	if _, ok := all[cp.InstanceID]; !ok && len(all) >= s.max {
		return &Error{Op: "FileStore.Save", Err: ErrBoundExceeded, Message: "max instances"}
	}
	cp.UpdatedAt = time.Now().UTC()
	all[cp.InstanceID] = cp
	return s.writeLocked(all)
}

func (s *FileStore) Load(instanceID string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return Checkpoint{}, err
	}
	cp, ok := all[instanceID]
	if !ok {
		return Checkpoint{}, &Error{Op: "FileStore.Load", Err: ErrNotFound, Message: "not found"}
	}
	return cloneCheckpoint(cp), nil
}

func (s *FileStore) Delete(instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return err
	}
	delete(all, instanceID)
	return s.writeLocked(all)
}

func (s *FileStore) List() ([]Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Checkpoint, 0, len(all))
	for _, cp := range all {
		out = append(out, cloneCheckpoint(cp))
	}
	return out, nil
}

func (s *FileStore) loadLocked() (map[string]Checkpoint, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Checkpoint{}, nil
		}
		return nil, err
	}
	var all map[string]Checkpoint
	if len(raw) == 0 {
		return map[string]Checkpoint{}, nil
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	if all == nil {
		all = map[string]Checkpoint{}
	}
	return all, nil
}

func (s *FileStore) writeLocked(all map[string]Checkpoint) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil && filepath.Dir(s.path) != "." {
		return err
	}
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// Best-effort directory fsync for rename durability.
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// JournalStore appends NDJSON checkpoint snapshots and recovers the latest
// state per instance on open. Deletes are recorded as tombstones.
type JournalStore struct {
	path string
	mu   sync.Mutex
	max  int
	data map[string]Checkpoint
}

type journalRecord struct {
	Op         string     `json:"op"` // "save" | "delete"
	Checkpoint Checkpoint `json:"checkpoint,omitempty"`
	InstanceID string     `json:"instance_id,omitempty"`
	At         time.Time  `json:"at"`
}

// NewJournalStore opens (or creates) a journal-backed store and replays it.
func NewJournalStore(path string, max int) (*JournalStore, error) {
	if max <= 0 {
		max = DefaultMaxInstances
	}
	s := &JournalStore{path: path, max: max, data: make(map[string]Checkpoint)}
	if err := s.replay(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *JournalStore) replay() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := splitLines(raw)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var rec journalRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // skip corrupt trailing line after crash
		}
		switch rec.Op {
		case "save":
			s.data[rec.Checkpoint.InstanceID] = cloneCheckpoint(rec.Checkpoint)
		case "delete":
			delete(s.data, rec.InstanceID)
		}
	}
	return nil
}

func (s *JournalStore) Save(cp Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[cp.InstanceID]; !ok && len(s.data) >= s.max {
		return &Error{Op: "JournalStore.Save", Err: ErrBoundExceeded, Message: "max instances"}
	}
	cp.UpdatedAt = time.Now().UTC()
	s.data[cp.InstanceID] = cloneCheckpoint(cp)
	return s.appendLocked(journalRecord{Op: "save", Checkpoint: cp, At: cp.UpdatedAt})
}

func (s *JournalStore) Load(instanceID string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.data[instanceID]
	if !ok {
		return Checkpoint{}, &Error{Op: "JournalStore.Load", Err: ErrNotFound, Message: "not found"}
	}
	return cloneCheckpoint(cp), nil
}

func (s *JournalStore) Delete(instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, instanceID)
	return s.appendLocked(journalRecord{Op: "delete", InstanceID: instanceID, At: time.Now().UTC()})
}

func (s *JournalStore) List() ([]Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Checkpoint, 0, len(s.data))
	for _, cp := range s.data {
		out = append(out, cloneCheckpoint(cp))
	}
	return out, nil
}

func (s *JournalStore) appendLocked(rec journalRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil && filepath.Dir(s.path) != "." {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func splitLines(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range raw {
		if c == '\n' {
			lines = append(lines, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}

func cloneCheckpoint(cp Checkpoint) Checkpoint {
	out := cp
	if cp.Steps != nil {
		out.Steps = make(map[string]StepState, len(cp.Steps))
		for k, v := range cp.Steps {
			st := v
			if v.Evidence != nil {
				st.Evidence = append([]Evidence(nil), v.Evidence...)
			}
			out.Steps[k] = st
		}
	}
	if cp.CompletedSteps != nil {
		out.CompletedSteps = append([]string(nil), cp.CompletedSteps...)
	}
	if cp.EpochAtStep != nil {
		out.EpochAtStep = make(map[string]uint64, len(cp.EpochAtStep))
		for k, v := range cp.EpochAtStep {
			out.EpochAtStep[k] = v
		}
	}
	if cp.Attrs != nil {
		out.Attrs = make(map[string]string, len(cp.Attrs))
		for k, v := range cp.Attrs {
			out.Attrs[k] = v
		}
	}
	return out
}
