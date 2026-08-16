package mobile

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStartStopLifecycle(t *testing.T) {
	root := t.TempDir()
	addr := Start(root, "", 0)
	if addr == "" {
		t.Fatal("Start returned empty address")
	}
	// Port 0: StartAsync retries port+1 semantics don't apply, but net.Listen
	// with :0 picks a free port either way; addr must be a URL.
	if !strings.HasPrefix(addr, "http://") {
		t.Fatalf("addr=%q", addr)
	}

	// Idempotent while running.
	if again := Start(root, "", 0); again != addr {
		t.Fatalf("second Start=%q, want %q", again, addr)
	}
	if Address() != addr {
		t.Fatalf("Address=%q", Address())
	}

	// Server actually answers.
	httpAddr := strings.Replace(addr, "0.0.0.0", "127.0.0.1", 1)
	httpAddr = strings.Replace(httpAddr, "[::]", "127.0.0.1", 1)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(httpAddr + "/api/auth/login")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not answering at %s", httpAddr)
		}
		time.Sleep(100 * time.Millisecond)
	}

	Stop()
	if Address() != "" {
		t.Fatal("Address should reset after Stop")
	}
	Stop() // safe no-op
}
