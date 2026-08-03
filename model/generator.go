package model

import (
	"math/rand"
	"time"
)

// NewWorld creates an empty model world.
func NewWorld(leaseTTL, transferTO int64) *World {
	if leaseTTL <= 0 {
		leaseTTL = 15
	}
	if transferTO <= 0 {
		transferTO = 30
	}
	return &World{
		LeaseTTL:       leaseTTL,
		TransferTO:     transferTO,
		Gens:           map[string]*Generation{},
		Claims:         map[string]*Claim{},
		ProtectedEpoch: map[string]uint64{},
	}
}

// Generator produces randomized legal-ish action sequences.
type Generator struct {
	rng    *rand.Rand
	claim  string
	gens   []string
}

// NewGenerator creates a sequence generator.
func NewGenerator(seed int64, claim string, gens []string) *Generator {
	return &Generator{rng: rand.New(rand.NewSource(seed)), claim: claim, gens: gens}
}

// Next returns the next random action.
func (g *Generator) Next() Action {
	types := []ActionType{
		ActRegisterGeneration, ActPassReadiness, ActRequestClaim, ActRenewClaim,
		ActBeginDrain, ActCompleteDrain, ActPrepareTransfer, ActCommitTransfer,
		ActAbortTransfer, ActDisconnect, ActReconnect, ActAdvanceTime,
		ActExpireLease, ActCrashOwner, ActCrashCandidate, ActReleaseClaim,
		ActPause, ActResume, ActRestartBackend,
	}
	t := types[g.rng.Intn(len(types))]
	gen := g.gens[g.rng.Intn(len(g.gens))]
	succ := g.gens[g.rng.Intn(len(g.gens))]
	a := Action{Type: t, Generation: gen, Claim: g.claim, Successor: succ}
	switch t {
	case ActAdvanceTime:
		a.Delta = time.Duration(g.rng.Intn(20)+1) * time.Millisecond
	case ActRequestClaim, ActCommitTransfer, ActReleaseClaim:
		a.OpID = fmtOp(g.rng.Int63())
	}
	return a
}

// Sequence returns n actions, ensuring gens are registered first.
func (g *Generator) Sequence(n int) []Action {
	out := make([]Action, 0, n+len(g.gens))
	for _, id := range g.gens {
		out = append(out, Action{Type: ActRegisterGeneration, Generation: id})
		out = append(out, Action{Type: ActPassReadiness, Generation: id})
	}
	for i := 0; i < n; i++ {
		out = append(out, g.Next())
	}
	return out
}

func fmtOp(n int64) string {
	if n < 0 {
		n = -n
	}
	return "op-" + itoa(uint64(n))
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
