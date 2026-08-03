package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/theworker02/shiftlock/resource"
)

func TestPathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	id := resource.MustParseResourceID("filesystem/test/demo/exports")
	r, err := New(Config{ID: id, Path: dir, Hardened: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve("../etc/passwd"); err == nil {
		t.Fatal("expected traversal reject")
	}
	if _, err := r.Resolve("/etc/passwd"); err == nil {
		t.Fatal("expected abs reject")
	}
	if r.Capabilities().SupportsFencing {
		t.Fatal("must not claim fencing")
	}
}

func TestExclusiveOwnershipAndAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	id := resource.MustParseResourceID("filesystem/test/demo/exports")
	r, err := New(Config{ID: id, Path: dir, Hardened: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := r.AcquireExclusive("worker-a"); err != nil {
		t.Fatal(err)
	}
	if err := r.AcquireExclusive("worker-b"); err == nil {
		t.Fatal("expected exclusive conflict")
	}
	payload := []byte("hello-shiftlock")
	if err := r.AtomicReplace("out/data.txt", payload); err != nil {
		t.Fatal(err)
	}
	sum, err := r.ChecksumSHA256("out/data.txt")
	if err != nil {
		t.Fatal(err)
	}
	if sum == "" {
		t.Fatal("empty checksum")
	}
	b, err := os.ReadFile(filepath.Join(dir, "out", "data.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(payload) {
		t.Fatalf("got %q", b)
	}
	if err := r.ReleaseExclusive("worker-a"); err != nil {
		t.Fatal(err)
	}
}
