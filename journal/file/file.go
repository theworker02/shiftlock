package file

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/theworker02/shiftlock/journal"
)

// Journal appends NDJSON entries to a file.
type Journal struct {
	mu   sync.Mutex
	path string
	f    *os.File
	seq  uint64
}

// Open creates or appends an NDJSON journal at path.
func Open(path string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	j := &Journal{path: path, f: f}
	// count existing lines for seq
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		j.seq++
	}
	return j, nil
}

func (j *Journal) Append(_ context.Context, e journal.Entry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	e.Seq = j.seq
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = j.f.Write(append(b, '\n'))
	return err
}

func (j *Journal) Read(_ context.Context, fromSeq uint64, limit int) ([]journal.Entry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := os.Open(j.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []journal.Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e journal.Entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Seq < fromSeq {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, sc.Err()
}

func (j *Journal) Export(ctx context.Context, fromSeq uint64) ([]journal.Entry, error) {
	return j.Read(ctx, fromSeq, 0)
}

func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

var _ journal.Journal = (*Journal)(nil)
