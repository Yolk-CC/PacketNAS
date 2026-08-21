// Package config parses CLI flags into a validated Config.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds the runtime configuration for PocketNAS.
type Config struct {
	Root     string // storage root; must exist; all file ops confined to it
	Addr     string // listen address, default "0.0.0.0"
	Port     int    // default 8080; auto-increment if occupied (max +100)
	Password string // optional; non-empty enables token auth
	Name     string // server name reported by /api/system/info and LAN discovery
	// OnnxLibPath optionally points at the onnxruntime shared library
	// (M11; Android passes context.nativeLibraryDir's libonnxruntime.so).
	OnnxLibPath string
}

// Parse parses args (e.g. os.Args[1:]) into a Config and validates Root.
func Parse(args []string) (Config, error) {
	fs := flag.NewFlagSet("pocket-nas", flag.ContinueOnError)
	root := fs.String("root", "", "storage root directory (required)")
	addr := fs.String("addr", "0.0.0.0", "listen address")
	port := fs.Int("port", 8080, "listen port")
	password := fs.String("password", "", "optional password to enable auth")
	hostname, _ := os.Hostname()
	name := fs.String("name", hostname, "server name (system info + LAN discovery)")
	onnxlib := fs.String("onnxlib", "", "path to the onnxruntime shared library (face recognition)")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Addr:        *addr,
		Port:        *port,
		Password:    *password,
		Name:        *name,
		OnnxLibPath: *onnxlib,
	}

	if *root == "" {
		return cfg, errors.New("-root is required")
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return cfg, fmt.Errorf("invalid root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return cfg, fmt.Errorf("root not accessible: %w", err)
	}
	if !info.IsDir() {
		return cfg, fmt.Errorf("root %q is not a directory", abs)
	}
	cfg.Root = abs
	return cfg, nil
}
