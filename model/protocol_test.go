package model_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/theworker02/shiftlock/model"
)

var seedFlag = flag.Int64("shiftlock.seed", 0, "repro seed for model random tests")

func TestInvariantsHappyPath(t *testing.T) {
	w := model.NewWorld(15, 30)
	acts := []model.Action{
		{Type: model.ActRegisterGeneration, Generation: "a"},
		{Type: model.ActRegisterGeneration, Generation: "b"},
		{Type: model.ActPassReadiness, Generation: "a"},
		{Type: model.ActRequestClaim, Generation: "a", Claim: "c", OpID: "acq1"},
		{Type: model.ActBeginDrain, Generation: "a"},
		{Type: model.ActPrepareTransfer, Generation: "a", Claim: "c", Successor: "b"},
		{Type: model.ActCommitTransfer, Generation: "a", Claim: "c", Successor: "b", OpID: "c1"},
	}
	prev := w.TokenSnapshot()
	for _, a := range acts {
		_ = w.Apply(a)
		if inv, detail := model.CheckTokenMonotonic(prev, w.TokenSnapshot()); inv != "" {
			t.Fatalf("%s: %s", inv, detail)
		}
		prev = w.TokenSnapshot()
		if inv, detail := w.CheckInvariants(); inv != "" {
			t.Fatalf("%s: %s", inv, detail)
		}
	}
	cl := w.Claims["c"]
	if cl.Owner != "b" || cl.Token != 2 {
		t.Fatalf("%+v", cl)
	}
}

func TestAbortNoAdvance(t *testing.T) {
	w := model.NewWorld(15, 30)
	_ = w.Apply(model.Action{Type: model.ActRegisterGeneration, Generation: "a"})
	_ = w.Apply(model.Action{Type: model.ActRegisterGeneration, Generation: "b"})
	_ = w.Apply(model.Action{Type: model.ActRequestClaim, Generation: "a", Claim: "c"})
	tok := w.Claims["c"].Token
	_ = w.Apply(model.Action{Type: model.ActPrepareTransfer, Generation: "a", Claim: "c", Successor: "b"})
	_ = w.Apply(model.Action{Type: model.ActAbortTransfer, Generation: "a", Claim: "c", Successor: "b"})
	if w.Claims["c"].Token != tok || w.Claims["c"].Owner != "a" || w.Claims["c"].Phase != model.PhaseOwned {
		t.Fatalf("%+v", w.Claims["c"])
	}
}

func TestIdempotentAcquire(t *testing.T) {
	w := model.NewWorld(15, 30)
	_ = w.Apply(model.Action{Type: model.ActRegisterGeneration, Generation: "a"})
	_ = w.Apply(model.Action{Type: model.ActRequestClaim, Generation: "a", Claim: "c", OpID: "x"})
	tok := w.Claims["c"].Token
	_ = w.Apply(model.Action{Type: model.ActReleaseClaim, Generation: "a", Claim: "c", OpID: "r1"})
	_ = w.Apply(model.Action{Type: model.ActRequestClaim, Generation: "a", Claim: "c", OpID: "x"}) // replay
	if w.Claims["c"].Token != tok {
		t.Fatalf("idempotent replay advanced token: %d vs %d", w.Claims["c"].Token, tok)
	}
}

func TestRandomSequences(t *testing.T) {
	seed := *seedFlag
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	const nSeq = 50
	const nActs = 200
	for i := 0; i < nSeq; i++ {
		s := seed + int64(i)
		w := model.NewWorld(15, 30)
		gen := model.NewGenerator(s, "billing", []string{"g0", "g1", "g2"})
		seq := gen.Sequence(nActs)
		prev := w.TokenSnapshot()
		for _, a := range seq {
			_ = w.Apply(a) // errors expected for illegal actions
			if inv, detail := model.CheckTokenMonotonic(prev, w.TokenSnapshot()); inv != "" {
				fail(t, s, "billing", seq, inv, detail)
			}
			prev = w.TokenSnapshot()
			if inv, detail := w.CheckInvariants(); inv != "" {
				fail(t, s, "billing", seq, inv, detail)
			}
		}
	}
}

func fail(t *testing.T, seed int64, claim string, acts []model.Action, inv, detail string) {
	t.Helper()
	rec := model.FailureRecord{Seed: seed, Claim: claim, Actions: acts, Invariant: inv, Detail: detail}
	dir := filepath.Join("testdata", "failures")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, fmt.Sprintf("%d.json", seed))
	_ = os.WriteFile(path, []byte(rec.JSON()), 0o644)
	t.Fatalf("invariant %s: %s\n%s\nwrote %s\n%s", inv, detail, rec.ReproduceCmd(), path, truncate(rec.JSON(), 2000))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ = json.Marshal
