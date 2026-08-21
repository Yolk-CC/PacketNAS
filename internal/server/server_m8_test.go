package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"pocket-nas/internal/config"
	"pocket-nas/internal/files"
)

// SPEC-M8 §1: /api/system/info reports serverName and apiLevel.
func TestSystemInfoServerNameAndAPILevel(t *testing.T) {
	cfg := config.Config{Root: t.TempDir(), Addr: "127.0.0.1", Port: 0, Name: "testbox"}
	r := NewRouter(cfg, files.New(cfg.Root))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/system/info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp struct {
		ServerName string `json:"serverName"`
		APILevel   int    `json:"apiLevel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ServerName != "testbox" {
		t.Fatalf("serverName=%q, want testbox", resp.ServerName)
	}
	if resp.APILevel != files.APILevel {
		t.Fatalf("apiLevel=%d, want %d", resp.APILevel, files.APILevel)
	}
}

// SPEC-M8 §1: a UDP "POCKETNAS_DISCOVER" probe gets a
// "POCKETNAS_HERE|<name>|<port>|<apiLevel>" reply.
func TestDiscoveryReply(t *testing.T) {
	d := startDiscovery("testbox", 8080)
	if d == nil {
		t.Skipf("udp :%d unavailable", DiscoveryPort)
	}
	defer d.Close()

	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: DiscoveryPort})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("POCKETNAS_DISCOVER")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no discovery reply: %v", err)
	}
	want := fmt.Sprintf("POCKETNAS_HERE|testbox|8080|%d", files.APILevel)
	if got := string(buf[:n]); got != want {
		t.Fatalf("reply=%q, want %q", got, want)
	}

	// Non-probe datagrams are ignored (no reply).
	if _, err := conn.Write([]byte("NOPE")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("unexpected reply to non-probe datagram")
	}
}

// SPEC-M8 §1: a bind failure disables discovery without error.
func TestDiscoveryBindFailureNonFatal(t *testing.T) {
	d := startDiscovery("a", 1)
	if d == nil {
		t.Skipf("udp :%d unavailable", DiscoveryPort)
	}
	defer d.Close()
	if d2 := startDiscovery("b", 2); d2 != nil {
		d2.Close()
		t.Fatal("second bind should fail (port in use)")
	}
}
