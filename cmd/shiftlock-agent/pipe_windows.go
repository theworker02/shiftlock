//go:build windows

package main

import (
	"fmt"
	"net"
)

// listenNamedPipe is stubbed without third-party pipe libraries.
// Operators should prefer a Unix agent host or enable explicit TCP only in lab
// with SHIFTLOCK_AGENT_ALLOW_TCP=1 (never production default).
func listenNamedPipe(addr string) (net.Listener, error) {
	return nil, fmt.Errorf("windows named pipe listener is stubbed at %s; use a POSIX host for unix sockets or lab-only TCP with SHIFTLOCK_AGENT_ALLOW_TCP=1", addr)
}
