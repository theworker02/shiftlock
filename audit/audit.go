// Package audit provides a tamper-evident, hash-chained audit log.
//
// Verification detects mutation, removal, sequence gaps, invalid signatures,
// and duplicate operation IDs. The log is tamper-evident, not tamper-proof.
package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/theworker02/shiftlock/security/signing"
)

var (
	ErrEmptyStore     = errors.New("audit: empty store")
	ErrChainBroken    = errors.New("audit: hash chain broken")
	ErrSequenceGap    = errors.New("audit: sequence gap")
	ErrDuplicateOp    = errors.New("audit: duplicate operation id")
	ErrInvalidSig     = errors.New("audit: invalid signature")
	ErrMutation       = errors.New("audit: record mutated")
	ErrRemoval        = errors.New("audit: record removed")
	ErrBadTimestamp   = errors.New("audit: invalid timestamp")
	ErrUnknownKey     = errors.New("audit: unknown signing key")
)

// Actor identifies who performed an action.
type Actor struct {
	ID   string `json:"id"`
	Type string `json:"type,omitempty"`
}

// Record is one append-only audit entry with hash chaining.
type Record struct {
	Sequence     uint64            `json:"sequence"`
	PreviousHash [32]byte          `json:"previous_hash"`
	RecordHash   [32]byte          `json:"record_hash"`
	Time         time.Time         `json:"time"`
	Actor        Actor             `json:"actor"`
	Action       string            `json:"action"`
	Resource     string            `json:"resource,omitempty"`
	Result       string            `json:"result,omitempty"`
	OperationID  string            `json:"operation_id,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Signature    []byte            `json:"signature,omitempty"`
	KeyID        string            `json:"key_id,omitempty"`
}

// bodyForHash is the canonical content hashed into RecordHash (excludes RecordHash/Signature).
type bodyForHash struct {
	Sequence     uint64            `json:"sequence"`
	PreviousHash string            `json:"previous_hash"`
	Time         time.Time         `json:"time"`
	Actor        Actor             `json:"actor"`
	Action       string            `json:"action"`
	Resource     string            `json:"resource,omitempty"`
	Result       string            `json:"result,omitempty"`
	OperationID  string            `json:"operation_id,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func hashBody(r Record) ([32]byte, error) {
	meta := r.Metadata
	if meta != nil {
		// copy sorted via canonical JSON
		cp := make(map[string]string, len(meta))
		for k, v := range meta {
			cp[k] = v
		}
		meta = cp
	}
	body := bodyForHash{
		Sequence:     r.Sequence,
		PreviousHash: hex.EncodeToString(r.PreviousHash[:]),
		Time:         r.Time.UTC(),
		Actor:        r.Actor,
		Action:       r.Action,
		Resource:     r.Resource,
		Result:       r.Result,
		OperationID:  r.OperationID,
		Metadata:     meta,
	}
	raw, err := signing.CanonicalJSON(body)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

// ComputeHash fills RecordHash from chain fields.
func ComputeHash(r *Record) error {
	h, err := hashBody(*r)
	if err != nil {
		return err
	}
	r.RecordHash = h
	return nil
}

// Finding is one verification problem.
type Finding struct {
	Code     string `json:"code"`
	Sequence uint64 `json:"sequence,omitempty"`
	Message  string `json:"message"`
}

// Report summarizes verification.
type Report struct {
	OK       bool      `json:"ok"`
	Records  int       `json:"records"`
	Findings []Finding `json:"findings,omitempty"`
}

// Store is an append-only in-memory audit log with optional file mirror.
type Store struct {
	mu      sync.Mutex
	records []Record
	ops     map[string]uint64
	keys    *signing.KeyRing
	signer  *signing.PrivateKey
	path    string
}

// Option configures Store.
type Option func(*Store)

// WithFile mirrors records as NDJSON to path.
func WithFile(path string) Option {
	return func(s *Store) { s.path = path }
}

// WithSigner enables record signatures.
func WithSigner(key signing.PrivateKey, ring *signing.KeyRing) Option {
	return func(s *Store) {
		s.signer = &key
		s.keys = ring
	}
}

// WithKeyRing sets verification keys without requiring a local signer.
func WithKeyRing(ring *signing.KeyRing) Option {
	return func(s *Store) { s.keys = ring }
}

// New creates an empty store.
func New(opts ...Option) *Store {
	s := &Store{ops: make(map[string]uint64)}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Append adds a record, computing sequence and hashes.
func (s *Store) Append(actor Actor, action, resource, result, operationID string, metadata map[string]string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if operationID != "" {
		if seq, ok := s.ops[operationID]; ok {
			return Record{}, fmt.Errorf("%w: %s (seq %d)", ErrDuplicateOp, operationID, seq)
		}
	}

	var prev [32]byte
	seq := uint64(1)
	if n := len(s.records); n > 0 {
		prev = s.records[n-1].RecordHash
		seq = s.records[n-1].Sequence + 1
	}

	rec := Record{
		Sequence:     seq,
		PreviousHash: prev,
		Time:         time.Now().UTC(),
		Actor:        actor,
		Action:       action,
		Resource:     resource,
		Result:       result,
		OperationID:  operationID,
		Metadata:     copyMeta(metadata),
	}
	if err := ComputeHash(&rec); err != nil {
		return Record{}, err
	}
	if s.signer != nil {
		sig, err := signing.SignBytes(*s.signer, rec.RecordHash[:])
		if err != nil {
			return Record{}, err
		}
		rec.Signature = sig.Sig
		rec.KeyID = string(sig.KeyID)
	}
	s.records = append(s.records, rec)
	if operationID != "" {
		s.ops[operationID] = seq
	}
	if s.path != "" {
		if err := appendFile(s.path, rec); err != nil {
			return rec, err
		}
	}
	return rec, nil
}

// Checkpoint appends a signed checkpoint over the current tip.
func (s *Store) Checkpoint(actor Actor) (Record, error) {
	s.mu.Lock()
	tip := ""
	n := len(s.records)
	if n > 0 {
		tip = hex.EncodeToString(s.records[n-1].RecordHash[:])
	}
	s.mu.Unlock()
	meta := map[string]string{"checkpoint": "true", "tip_hash": tip}
	return s.Append(actor, "audit.checkpoint", "", "ok", "", meta)
}

// Records returns a copy of all records.
func (s *Store) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records))
	copy(out, s.records)
	return out
}

// Verify checks the in-memory chain.
func (s *Store) Verify() Report {
	s.mu.Lock()
	recs := make([]Record, len(s.records))
	copy(recs, s.records)
	keys := s.keys
	s.mu.Unlock()
	return VerifyRecords(recs, keys)
}

// VerifyRecords validates an exported chain.
func VerifyRecords(recs []Record, keys *signing.KeyRing) Report {
	rep := Report{Records: len(recs), OK: true}
	if len(recs) == 0 {
		return rep
	}
	seenOps := make(map[string]uint64)
	var expectPrev [32]byte
	var expectSeq uint64 = 1

	for i, r := range recs {
		if r.Sequence != expectSeq {
			rep.OK = false
			if r.Sequence > expectSeq {
				rep.Findings = append(rep.Findings, Finding{
					Code: "sequence_gap", Sequence: r.Sequence,
					Message: fmt.Sprintf("expected sequence %d, got %d (possible removal)", expectSeq, r.Sequence),
				})
			} else {
				rep.Findings = append(rep.Findings, Finding{
					Code: "sequence_mismatch", Sequence: r.Sequence,
					Message: fmt.Sprintf("expected sequence %d, got %d", expectSeq, r.Sequence),
				})
			}
		}
		if !bytes.Equal(r.PreviousHash[:], expectPrev[:]) {
			rep.OK = false
			code := "chain_broken"
			if i > 0 {
				code = "mutation_or_removal"
			}
			rep.Findings = append(rep.Findings, Finding{
				Code: code, Sequence: r.Sequence,
				Message: "previous_hash does not match prior record_hash",
			})
		}
		want, err := hashBody(r)
		if err != nil {
			rep.OK = false
			rep.Findings = append(rep.Findings, Finding{Code: "hash_error", Sequence: r.Sequence, Message: err.Error()})
		} else if want != r.RecordHash {
			rep.OK = false
			rep.Findings = append(rep.Findings, Finding{
				Code: "mutation", Sequence: r.Sequence,
				Message: "record_hash does not match canonical body",
			})
		}
		if r.Time.IsZero() {
			rep.OK = false
			rep.Findings = append(rep.Findings, Finding{Code: "bad_timestamp", Sequence: r.Sequence, Message: ErrBadTimestamp.Error()})
		}
		if r.OperationID != "" {
			if prev, ok := seenOps[r.OperationID]; ok {
				rep.OK = false
				rep.Findings = append(rep.Findings, Finding{
					Code: "duplicate_op_id", Sequence: r.Sequence,
					Message: fmt.Sprintf("operation_id %q also at sequence %d", r.OperationID, prev),
				})
			}
			seenOps[r.OperationID] = r.Sequence
		}
		if len(r.Signature) > 0 {
			if keys == nil || keys.Len() == 0 {
				rep.OK = false
				rep.Findings = append(rep.Findings, Finding{
					Code: "unknown_key", Sequence: r.Sequence, Message: ErrUnknownKey.Error(),
				})
			} else {
				sig := signing.Signature{
					KeyID:     signing.KeyID(r.KeyID),
					Algorithm: signing.AlgorithmEd25519,
					Version:   1,
					Sig:       r.Signature,
					SignedAt:  r.Time,
				}
				if err := signing.VerifyBytes(keys, r.RecordHash[:], sig); err != nil {
					rep.OK = false
					code := "invalid_signature"
					if errors.Is(err, signing.ErrUnknownKey) {
						code = "unknown_key"
					}
					rep.Findings = append(rep.Findings, Finding{
						Code: code, Sequence: r.Sequence, Message: err.Error(),
					})
				}
			}
		}
		expectPrev = r.RecordHash
		expectSeq = r.Sequence + 1
	}

	// Detect trailing removal: if sequences skip inside list we already flagged;
	// also flag if sequences are not strictly contiguous from 1.
	if len(recs) > 0 && recs[0].Sequence != 1 {
		rep.OK = false
		rep.Findings = append(rep.Findings, Finding{
			Code: "removal", Sequence: recs[0].Sequence,
			Message: "chain does not start at sequence 1",
		})
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].Sequence == rep.Findings[j].Sequence {
			return rep.Findings[i].Code < rep.Findings[j].Code
		}
		return rep.Findings[i].Sequence < rep.Findings[j].Sequence
	})
	return rep
}

// ExportJSON writes records as pretty JSON.
func ExportJSON(recs []Record) ([]byte, error) {
	return json.MarshalIndent(recs, "", "  ")
}

// LoadNDJSON loads records from an NDJSON file.
func LoadNDJSON(path string) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func appendFile(path string, r Record) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(r)
}

func copyMeta(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
