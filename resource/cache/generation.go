// Package cache provides shared helpers for cache resource adapters.
package cache

import (
	"context"
	"errors"
	"fmt"

	"github.com/theworker02/shiftlock/resource"
)

var (
	ErrNilController = errors.New("cache: nil generation controller")
	ErrVerifyFailed  = errors.New("cache: generation verify failed")
	ErrAborted       = errors.New("cache: generation aborted")
)

// GenerationController is implemented by memory (and similar) cache adapters.
type GenerationController interface {
	BuildGeneration(ctx context.Context) (uint64, error)
	ActivateGeneration(gen uint64, seed map[string]string) error
	AbortGeneration()
	Generation() uint64
}

// BuildFunc populates a reserved generation's seed data.
type BuildFunc func(ctx context.Context, gen uint64) (map[string]string, error)

// VerifyFunc checks a built seed before activation.
type VerifyFunc func(ctx context.Context, gen uint64, seed map[string]string) error

// EpochAdvancer optionally advances a registry epoch after activation.
type EpochAdvancer interface {
	Advance(id resource.ResourceID, reason string) (resource.EpochAdvance, error)
}

// RetireFunc cleans up prior generation artifacts after activation.
type RetireFunc func(ctx context.Context, previous uint64) error

// FlowOptions configures a generation rebuild flow.
type FlowOptions struct {
	ResourceID resource.ResourceID
	Build      BuildFunc
	Verify     VerifyFunc
	Retire     RetireFunc
	Epochs     EpochAdvancer
	DryRun     bool
}

// FlowResult is a sanitized outcome (no cache values).
type FlowResult struct {
	Previous  uint64                `json:"previous"`
	Reserved  uint64                `json:"reserved"`
	Activated uint64                `json:"activated,omitempty"`
	DryRun    bool                  `json:"dry_run,omitempty"`
	Epoch     resource.EpochAdvance `json:"epoch,omitempty"`
	Retired   bool                  `json:"retired,omitempty"`
}

// RunGenerationFlow executes reserve → build → verify → activate → epoch → retire.
// On any failure after reserve, AbortGeneration is called.
func RunGenerationFlow(ctx context.Context, ctrl GenerationController, opts FlowOptions) (FlowResult, error) {
	if ctrl == nil {
		return FlowResult{}, ErrNilController
	}
	if opts.Build == nil {
		return FlowResult{}, errors.New("cache: Build required")
	}
	prev := ctrl.Generation()
	gen, err := ctrl.BuildGeneration(ctx)
	if err != nil {
		return FlowResult{}, err
	}
	res := FlowResult{Previous: prev, Reserved: gen, DryRun: opts.DryRun}
	abort := true
	defer func() {
		if abort {
			ctrl.AbortGeneration()
		}
	}()

	seed, err := opts.Build(ctx, gen)
	if err != nil {
		return res, err
	}
	if opts.Verify != nil {
		if err := opts.Verify(ctx, gen, seed); err != nil {
			return res, fmt.Errorf("%w: %v", ErrVerifyFailed, err)
		}
	}
	if opts.DryRun {
		abort = true // do not activate
		res.Activated = 0
		return res, nil
	}
	if err := ctrl.ActivateGeneration(gen, seed); err != nil {
		return res, err
	}
	abort = false
	res.Activated = gen

	if opts.Epochs != nil && !opts.ResourceID.IsZero() {
		adv, err := opts.Epochs.Advance(opts.ResourceID, "cache-generation-activate")
		if err != nil {
			return res, err
		}
		res.Epoch = adv
	}
	if opts.Retire != nil {
		if err := opts.Retire(ctx, prev); err != nil {
			return res, err
		}
		res.Retired = true
	}
	return res, nil
}
