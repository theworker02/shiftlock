// Command shiftlock-agent is a local control-plane agent skeleton.
//
// Transport: Unix domain socket (posix) or Windows named pipe. NO default TCP.
// Auth is stubbed; message size limits are enforced.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const (
	defaultMaxMessageBytes = 1 << 20 // 1 MiB
	defaultReadTimeout     = 10 * time.Second
)

func main() {
	fs := flag.NewFlagSet("shiftlock-agent", flag.ExitOnError)
	listen := fs.String("listen", defaultListen(), "unix socket path or Windows pipe path (\\\\.\\pipe\\shiftlock)")
	maxMsg := fs.Int("max-message-bytes", defaultMaxMessageBytes, "maximum request/response size")
	token := fs.String("auth-token", "", "stub shared token (required for non-ping commands when set)")
	tcp := fs.Bool("tcp", false, "FORBIDDEN in production; refused unless SHIFTLOCK_AGENT_ALLOW_TCP=1")
	_ = fs.Parse(os.Args[1:])

	if *tcp {
		if os.Getenv("SHIFTLOCK_AGENT_ALLOW_TCP") != "1" {
			fmt.Fprintln(os.Stderr, "error: TCP listener disabled by default; refuse --tcp")
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "WARNING: TCP agent listener enabled (development only)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := listenLocal(ctx, *listen, *tcp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()
	fmt.Fprintf(os.Stderr, "shiftlock-agent listening on %s (max_message=%d)\n", ln.Addr(), *maxMsg)

	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			continue
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			handleConn(c, *maxMsg, *token)
		}(conn)
	}
	wg.Wait()
}

func defaultListen() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\shiftlock`
	}
	return "/tmp/shiftlock.sock"
}

func listenLocal(ctx context.Context, addr string, allowTCP bool) (net.Listener, error) {
	_ = ctx
	if allowTCP && stringsHasScheme(addr, "tcp://") {
		return net.Listen("tcp", stringsTrimPrefix(addr, "tcp://"))
	}
	if stringsHasScheme(addr, "tcp://") || looksTCP(addr) {
		return nil, errors.New("TCP not allowed; use a unix socket or Windows named pipe path")
	}
	if runtime.GOOS == "windows" {
		return listenNamedPipe(addr)
	}
	_ = os.Remove(addr)
	return net.Listen("unix", addr)
}

func looksTCP(addr string) bool {
	if addr == "" {
		return false
	}
	// host:port heuristic
	if len(addr) > 0 && addr[0] == ':' {
		return true
	}
	for i := 0; i < len(addr); i++ {
		if addr[i] == ':' {
			return true
		}
	}
	return false
}

func stringsHasScheme(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func stringsTrimPrefix(s, prefix string) string {
	if stringsHasScheme(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

type request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Token  string          `json:"token,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

type response struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

func handleConn(c net.Conn, maxMsg int, authToken string) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(defaultReadTimeout))
	limited := io.LimitReader(c, int64(maxMsg)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return
	}
	if len(data) > maxMsg {
		writeRes(c, response{OK: false, Error: "message too large"})
		return
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		writeRes(c, response{OK: false, Error: "invalid json"})
		return
	}
	res := dispatch(req, authToken)
	writeRes(c, res)
}

func dispatch(req request, authToken string) response {
	res := response{ID: req.ID}
	switch req.Method {
	case "ping":
		res.OK = true
		res.Payload = map[string]string{"pong": "ok"}
		return res
	case "version":
		if authToken != "" && req.Token != authToken {
			res.Error = "unauthorized"
			return res
		}
		res.OK = true
		res.Payload = map[string]string{"agent": "shiftlock-agent", "auth": "stub"}
		return res
	default:
		if authToken != "" && req.Token != authToken {
			res.Error = "unauthorized"
			return res
		}
		res.Error = "method not implemented (agent skeleton)"
		return res
	}
}

func writeRes(w io.Writer, res response) {
	b, _ := json.Marshal(res)
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}
