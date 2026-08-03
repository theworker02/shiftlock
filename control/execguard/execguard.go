// Package execguard runs only allowlisted absolute executables with exact
// argument patterns. No shell, no env inheritance by default.
package execguard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrDenied       = errors.New("execguard: command denied")
	ErrRelativePath = errors.New("execguard: relative executable path")
	ErrShell        = errors.New("execguard: shell execution forbidden")
	ErrOutputLimit  = errors.New("execguard: output size limit exceeded")
	ErrTimeout      = errors.New("execguard: execution timeout")
)

// CommandRule allowlists one absolute executable and exact argv patterns.
type CommandRule struct {
	Path        string
	AllowedArgs [][]string // each inner slice is a full argv after the executable
}

// Policy configures the guard.
type Policy struct {
	Commands       []CommandRule
	Timeout        time.Duration
	MaxOutputBytes int
	EnvAllowlist   []string // names copied from current env when set
	WorkDir        string   // if set, must be absolute
	DryRun         bool
}

// Guard evaluates and optionally runs allowlisted commands.
type Guard struct {
	policy Policy
}

// New validates policy and returns a Guard.
func New(policy Policy) (*Guard, error) {
	if policy.Timeout <= 0 {
		policy.Timeout = 30 * time.Second
	}
	if policy.MaxOutputBytes <= 0 {
		policy.MaxOutputBytes = 1 << 20 // 1 MiB
	}
	for i, c := range policy.Commands {
		if c.Path == "" || !filepath.IsAbs(c.Path) {
			return nil, fmt.Errorf("%w: rule %d", ErrRelativePath, i)
		}
		base := filepath.Base(c.Path)
		if isShell(base) {
			return nil, fmt.Errorf("%w: %s", ErrShell, c.Path)
		}
		if strings.ContainsAny(c.Path, "*?[]") {
			return nil, fmt.Errorf("%w: wildcards not allowed", ErrDenied)
		}
	}
	if policy.WorkDir != "" && !filepath.IsAbs(policy.WorkDir) {
		return nil, fmt.Errorf("%w: workdir must be absolute", ErrRelativePath)
	}
	return &Guard{policy: policy}, nil
}

func isShell(base string) bool {
	switch strings.ToLower(base) {
	case "sh", "bash", "zsh", "fish", "cmd.exe", "cmd", "powershell.exe", "powershell", "pwsh", "pwsh.exe":
		return true
	default:
		return false
	}
}

// Request is a proposed execution.
type Request struct {
	Path string
	Args []string
}

// Result is a sanitized execution outcome (no secrets assumed in stdout).
type Result struct {
	Path     string
	Args     []string
	DryRun   bool
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Validate checks whether the request would be allowed without running it.
func (g *Guard) Validate(req Request) error {
	if req.Path == "" || !filepath.IsAbs(req.Path) {
		return ErrRelativePath
	}
	if isShell(filepath.Base(req.Path)) {
		return ErrShell
	}
	for _, a := range req.Args {
		if strings.Contains(a, "\x00") {
			return ErrDenied
		}
	}
	for _, rule := range g.policy.Commands {
		if filepath.Clean(rule.Path) != filepath.Clean(req.Path) {
			continue
		}
		for _, allowed := range rule.AllowedArgs {
			if argsEqual(allowed, req.Args) {
				return nil
			}
		}
	}
	return ErrDenied
}

func argsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Run validates then executes (or dry-runs) the request.
func (g *Guard) Run(ctx context.Context, req Request) (Result, error) {
	if err := g.Validate(req); err != nil {
		return Result{}, err
	}
	res := Result{Path: req.Path, Args: append([]string(nil), req.Args...), DryRun: g.policy.DryRun}
	if g.policy.DryRun {
		return res, nil
	}
	ctx, cancel := context.WithTimeout(ctx, g.policy.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, req.Path, req.Args...)
	if g.policy.WorkDir != "" {
		cmd.Dir = g.policy.WorkDir
	}
	cmd.Env = minimalEnv(g.policy.EnvAllowlist)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: g.policy.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: g.policy.MaxOutputBytes}

	err := cmd.Run()
	res.Stdout = stdout.Bytes()
	res.Stderr = stderr.Bytes()
	if errors.Is(err, context.DeadlineExceeded) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return res, ErrTimeout
	}
	var lw *limitedWriter
	if errors.As(err, &lw) {
		return res, ErrOutputLimit
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return res, nil // exit code captured; caller inspects ExitCode
		}
		return res, err
	}
	return res, nil
}

func minimalEnv(allow []string) []string {
	out := []string{}
	// Never inherit full environment by default.
	for _, name := range allow {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

type limitedWriter struct {
	buf   *bytes.Buffer
	limit int
	n     int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.n+len(p) > w.limit {
		remain := w.limit - w.n
		if remain > 0 {
			_, _ = w.buf.Write(p[:remain])
			w.n += remain
		}
		return 0, ErrOutputLimit
	}
	n, err := w.buf.Write(p)
	w.n += n
	return n, err
}

func (w *limitedWriter) Error() string { return ErrOutputLimit.Error() }
