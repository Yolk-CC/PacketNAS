// Package mobile exposes PocketNAS to Android via gomobile bind:
//
//	gomobile bind -target=android/arm64 -androidapi 26 ./mobile
//
// Only basic types cross the binding boundary, per gomobile rules.
package mobile

import (
	"sync"

	"pocket-nas/internal/config"
	"pocket-nas/internal/server"
)

var (
	mu      sync.Mutex
	running bool
	addr    string
	stopFn  func()
)

// Start launches the PocketNAS server. root is the storage root (e.g.
// /storage/emulated/0), password may be empty, port is the preferred port
// (the next free port is used if occupied). onnxLibPath is the absolute
// path to libonnxruntime.so (Android: context.nativeLibraryDir +
// "/libonnxruntime.so"); pass "" to skip face recognition (the API then
// reports faces_unavailable). It returns the actual base URL
// (e.g. "http://0.0.0.0:8080"), or "" on failure. Calling Start while
// already running returns the current address without restarting.
func Start(root, password string, port int, onnxLibPath string) string {
	mu.Lock()
	defer mu.Unlock()
	if running {
		return addr
	}
	cfg := config.Config{
		Root:        root,
		Addr:        "0.0.0.0",
		Port:        port,
		Password:    password,
		Name:        "PocketNAS", // SPEC-M8: fixed server name on Android
		OnnxLibPath: onnxLibPath,
	}
	a, stop, err := server.StartAsync(cfg)
	if err != nil {
		return ""
	}
	addr, stopFn, running = a, stop, true
	return addr
}

// Stop gracefully shuts the server down. Safe to call when not running.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return
	}
	stopFn()
	stopFn, running, addr = nil, false, ""
}

// Address returns the current base URL, or "" when not running.
func Address() string {
	mu.Lock()
	defer mu.Unlock()
	return addr
}
