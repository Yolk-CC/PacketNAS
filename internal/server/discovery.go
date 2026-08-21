package server

import (
	"fmt"
	"log"
	"net"
	"strings"

	"pocket-nas/internal/files"
)

// LAN discovery protocol v1 (SPEC-M8 §1): a client broadcasts the text
// "POCKETNAS_DISCOVER" to UDP port 45777; the server replies to the sender
// with "POCKETNAS_HERE|<serverName>|<port>|<apiLevel>".
const (
	DiscoveryPort  = 45777
	discoverQuery  = "POCKETNAS_DISCOVER"
	discoverMaxLen = 256
)

// discovery answers LAN discovery probes. It runs in its own goroutine and
// never affects the main HTTP service: a bind failure only logs.
type discovery struct {
	conn *net.UDPConn
	done chan struct{}
}

// startDiscovery binds UDP DiscoveryPort and replies to discovery probes with
// the server name and the actual HTTP port. Returns nil on failure (logged).
func startDiscovery(serverName string, port int) *discovery {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: DiscoveryPort})
	if err != nil {
		log.Printf("discovery: listen udp :%d: %v (LAN discovery disabled)", DiscoveryPort, err)
		return nil
	}
	d := &discovery{conn: conn, done: make(chan struct{})}
	go d.serve(serverName, port)
	return d
}

func (d *discovery) serve(serverName string, port int) {
	reply := fmt.Sprintf("POCKETNAS_HERE|%s|%d|%d", serverName, port, files.APILevel)
	buf := make([]byte, discoverMaxLen)
	for {
		n, src, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-d.done:
				return // closed via Close
			default:
				log.Printf("discovery: read: %v", err)
				return
			}
		}
		if strings.TrimSpace(string(buf[:n])) != discoverQuery {
			continue
		}
		if _, err := d.conn.WriteToUDP([]byte(reply), src); err != nil {
			log.Printf("discovery: reply to %s: %v", src, err)
		}
	}
}

// Close stops the listener; it is idempotent.
func (d *discovery) Close() {
	select {
	case <-d.done:
	default:
		close(d.done)
		_ = d.conn.Close()
	}
}
