// Package promotion provides a skeleton for environment promotion workflows
// (e.g. staging → production) built on workflow definitions. Steps are
// app-supplied; this package only names phases and builds a definition shell.
package promotion

import (
	"errors"

	"github.com/theworker02/shiftlock/workflow"
)

var ErrInvalidArg = errors.New("promotion: invalid argument")

// Phase names the promotion lifecycle (documentation / attrs only).
type Phase string

const (
	PhasePlan       Phase = "plan"
	PhaseValidate   Phase = "validate"
	PhasePrepare    Phase = "prepare"
	PhasePromote    Phase = "promote"
	PhaseVerify     Phase = "verify"
	PhaseComplete   Phase = "complete"
	PhaseCompensate Phase = "compensate"
)

// Hooks are app-supplied promotion actions.
type Hooks struct {
	Validate  workflow.ActionFunc
	Prepare   workflow.ActionFunc
	Promote   workflow.ActionFunc
	Verify    workflow.ActionFunc
	Rollback  workflow.CompensateFunc // compensates Promote
}

// Config names the workflow and wires hooks.
type Config struct {
	Name        string // workflow name; default "promote"
	FromEnv     string
	ToEnv       string
	Hooks       Hooks
}

// BuildDefinition constructs a validated workflow Definition for promotion.
func BuildDefinition(cfg Config) (*workflow.Definition, error) {
	if cfg.Hooks.Validate == nil || cfg.Hooks.Prepare == nil || cfg.Hooks.Promote == nil || cfg.Hooks.Verify == nil {
		return nil, ErrInvalidArg
	}
	name := cfg.Name
	if name == "" {
		name = "promote"
	}
	b := workflow.Define(name).
		Step("validate", cfg.Hooks.Validate).
		Step("prepare", cfg.Hooks.Prepare).
		Step("promote", cfg.Hooks.Promote).
		Step("verify", cfg.Hooks.Verify).
		Depend("prepare", "validate").
		Depend("promote", "prepare").
		Depend("verify", "promote").
		Idempotency("promote", workflow.RequiresOperationID).
		Mutating("promote", true).
		Mutating("prepare", true)
	if cfg.Hooks.Rollback != nil {
		b = b.Compensate("promote", cfg.Hooks.Rollback)
	}
	def, err := b.Build()
	if err != nil {
		return nil, err
	}
	return def, nil
}
