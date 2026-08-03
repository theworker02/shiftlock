// Thin alias: prefer `shiftlock` for new workflows. Preserves inspect subcommands.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func maybeDelegateToShiftlock() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case "status", "claims", "generations", "tasks", "maintenance", "lockdown",
		"capabilities", "security", "audit", "snapshot", "version":
		return true
	default:
		return false
	}
}

func runShiftlockAlias() {
	exe, err := exec.LookPath("shiftlock")
	if err != nil {
		// Try sibling binary next to this executable.
		self, _ := os.Executable()
		cand := filepath.Join(filepath.Dir(self), "shiftlock")
		if runtimeGOOSWindows() {
			cand += ".exe"
		}
		if _, err2 := os.Stat(cand); err2 == nil {
			exe = cand
		} else {
			fmt.Fprintf(os.Stderr, "shiftlock-inspect: unified CLI not on PATH; continuing with inspect toolkit\n")
			return
		}
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "shiftlock alias: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runtimeGOOSWindows() bool {
	return os.PathSeparator == '\\' && os.Getenv("OS") == "Windows_NT"
}
