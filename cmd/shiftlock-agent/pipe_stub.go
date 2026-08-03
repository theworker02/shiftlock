//go:build !windows

package main

import "net"

func listenNamedPipe(addr string) (net.Listener, error) {
	return net.Listen("unix", addr)
}
