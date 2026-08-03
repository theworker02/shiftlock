package execguard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDenyRelativeAndShell(t *testing.T) {
	_, err := New(Policy{Commands: []CommandRule{{Path: "bin/tool", AllowedArgs: [][]string{{"ok"}}}}})
	if err == nil {
		t.Fatal("expected relative path reject")
	}
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = `C:\Windows\System32\cmd.exe`
	}
	_, err = New(Policy{Commands: []CommandRule{{Path: shell, AllowedArgs: [][]string{{"/c", "echo"}}}}})
	if err == nil {
		t.Fatal("expected shell reject")
	}
}

func TestAllowlistExactArgs(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ = filepath.Abs(exe)
	g, err := New(Policy{
		Commands: []CommandRule{{Path: exe, AllowedArgs: [][]string{{"-version"}}}},
		DryRun:   true,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(Request{Path: exe, Args: []string{"-version"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(Request{Path: exe, Args: []string{"-v"}}); err != ErrDenied {
		t.Fatalf("want denied, got %v", err)
	}
	res, err := g.Run(context.Background(), Request{Path: exe, Args: []string{"-version"}})
	if err != nil || !res.DryRun {
		t.Fatalf("dryrun res=%v err=%v", res, err)
	}
}
