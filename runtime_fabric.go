package shiftlock

import (
	"os"
	"path/filepath"
	"time"

	"github.com/theworker02/shiftlock/audit"
	"github.com/theworker02/shiftlock/control/lockdown"
	"github.com/theworker02/shiftlock/failover"
	"github.com/theworker02/shiftlock/migration"
	"github.com/theworker02/shiftlock/resource"
	syncpkg "github.com/theworker02/shiftlock/sync"
	"github.com/theworker02/shiftlock/workflow"
)

// LocalStateMode selects where Phase 7 local-first durable state is kept.
type LocalStateMode string

const (
	// LocalStateMemory keeps resource/workflow durable state in-process.
	LocalStateMemory LocalStateMode = "memory"
	// LocalStateFile keeps workflow checkpoints in a JSON file.
	LocalStateFile LocalStateMode = "file"
	// LocalStateDir keeps a local-first layout under Dir:
	//   Dir/workflows/checkpoints.journal  — journal-backed workflow store
	//   Dir/registry/events.ndjson         — resource registry event journal
	LocalStateDir LocalStateMode = "dir"
)

// LocalStateConfig is opt-in local-first fabric state.
type LocalStateConfig struct {
	Mode         LocalStateMode
	WorkflowPath string // used when Mode == LocalStateFile
	Dir          string // used when Mode == LocalStateDir (or with WithLocalStateDir)
	MaxInstances int
}

// WithLocalState returns a helper that enables resources/workflows and sets LocalState.
func WithLocalState(ls LocalStateConfig) func(*RuntimeConfig) {
	return func(cfg *RuntimeConfig) {
		cfg.LocalState = &ls
		cfg.EnableResources = true
		cfg.EnableWorkflows = true
	}
}

// WithLocalStateDir enables fabric with journal + checkpoints under dir.
func WithLocalStateDir(dir string) func(*RuntimeConfig) {
	return WithLocalState(LocalStateConfig{Mode: LocalStateDir, Dir: dir})
}

// resourceLockdown adapts lockdown.Manager to resource.LockdownChecker.
type resourceLockdown struct{ m *lockdown.Manager }

func (r resourceLockdown) BlocksMutations() bool {
	if r.m == nil || !r.m.Active() {
		return false
	}
	st := r.m.State()
	switch st.Mode {
	case lockdown.ModeFailClosed, lockdown.ModeFullService, lockdown.ModeIsolateClaims, lockdown.ModeRestricted:
		return true
	default:
		return false
	}
}

// workflowAudit adapts Runtime audit store.
type workflowAudit struct{ store *audit.Store }

func (a workflowAudit) Audit(actor, action, res, result, operationID string) {
	if a.store == nil {
		return
	}
	_, _ = a.store.Append(audit.Actor{ID: actor, Type: "workflow"}, action, res, result, operationID, nil)
}

// resourceLookup adapts resource.Registry for workflow capability checks.
type resourceLookup struct{ reg *resource.Registry }

func (l resourceLookup) Get(id resource.ResourceID) (*resource.Entry, error) {
	return l.reg.Get(id)
}

func (r *Runtime) initFabric() {
	enableRes := r.cfg.EnableResources || r.cfg.LocalState != nil || r.cfg.EnableWorkflows
	enableWF := r.cfg.EnableWorkflows || r.cfg.LocalState != nil
	if !enableRes && !enableWF {
		return
	}

	maxRes := r.cfg.MaxResources
	if maxRes <= 0 {
		maxRes = resource.DefaultMaxResources
	}
	var ld resource.LockdownChecker
	if r.lock != nil {
		ld = resourceLockdown{m: r.lock}
	}
	clock := func() time.Time {
		if r.coord != nil && r.coord.clock != nil {
			return r.coord.clock.Now()
		}
		return time.Now()
	}

	if enableRes {
		r.resources = resource.NewRegistry(resource.RegistryConfig{
			MaxResources: maxRes,
			Lockdown:     ld,
			Clock:        clock,
		})
		r.features.Resources = true
		if r.cfg.LocalState != nil && r.cfg.LocalState.Mode == LocalStateDir && r.cfg.LocalState.Dir != "" {
			regDir := filepath.Join(r.cfg.LocalState.Dir, "registry")
			_ = os.MkdirAll(regDir, 0o700)
			// Touch journal path so operators can attach observers later.
			regJournal := filepath.Join(regDir, "events.ndjson")
			if f, err := os.OpenFile(regJournal, os.O_CREATE|os.O_RDONLY, 0o600); err == nil {
				_ = f.Close()
			}
			r.localStateDir = r.cfg.LocalState.Dir
		}
	}

	if enableWF {
		var store workflow.Store
		maxInst := 0
		if r.cfg.LocalState != nil {
			maxInst = r.cfg.LocalState.MaxInstances
			switch r.cfg.LocalState.Mode {
			case LocalStateFile:
				if r.cfg.LocalState.WorkflowPath != "" {
					store = workflow.NewFileStore(r.cfg.LocalState.WorkflowPath, maxInst)
				}
			case LocalStateDir:
				dir := r.cfg.LocalState.Dir
				if dir != "" {
					wfDir := filepath.Join(dir, "workflows")
					_ = os.MkdirAll(wfDir, 0o700)
					js, err := workflow.NewJournalStore(filepath.Join(wfDir, "checkpoints.journal"), maxInst)
					if err == nil {
						store = js
					} else {
						store = workflow.NewFileStore(filepath.Join(wfDir, "checkpoints.json"), maxInst)
					}
					r.localStateDir = dir
				}
			}
		}
		if store == nil {
			store = workflow.NewMemoryStore(maxInst)
		}
		hooks := workflow.Hooks{Lockdown: ld}
		if r.audit != nil {
			hooks.Audit = workflowAudit{store: r.audit}
		}
		if r.resources != nil {
			hooks.Resources = resourceLookup{reg: r.resources}
		}
		r.workflows = workflow.NewEngine(workflow.EngineConfig{
			Store:   store,
			Hooks:   hooks,
			Clock:   clock,
			MaxRuns: maxInst,
		})
		r.features.Workflows = true
	}

	if r.resources != nil {
		migHooks := migration.Hooks{Lockdown: ld}
		r.migrations = migration.New(migration.Config{Hooks: migHooks, Clock: clock})
		r.failoverMgr = failover.NewManager(r.resources)
		eng, err := syncpkg.New(syncpkg.Config{Policy: syncpkg.PreferSource})
		if err == nil {
			r.syncEngine = eng
		}
	}
}

// LocalStateDir returns the configured local-first state directory (may be empty).
func (r *Runtime) LocalStateDir() string { return r.localStateDir }

// Resources returns the Runtime-owned resource registry (may be nil if not enabled).
func (r *Runtime) Resources() *resource.Registry { return r.resources }

// Workflows returns the workflow engine (may be nil if not enabled).
func (r *Runtime) Workflows() *workflow.Engine { return r.workflows }

// Migrations returns the migration coordinator (may be nil if resources not enabled).
func (r *Runtime) Migrations() *migration.Coordinator { return r.migrations }

// Failover returns the failover manager (may be nil if resources not enabled).
func (r *Runtime) Failover() *failover.Manager { return r.failoverMgr }

// Sync returns the sync engine façade (may be nil if resources not enabled).
func (r *Runtime) Sync() *syncpkg.Engine { return r.syncEngine }
