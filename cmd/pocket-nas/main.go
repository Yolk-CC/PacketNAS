// PocketNAS entry point: parses flags and starts the server.
package main

import (
	"fmt"
	"os"

	"pocket-nas/internal/config"
	"pocket-nas/internal/server"
)

// Version is the build version, injected at release time via
// -ldflags "-X main.Version=vX.Y.Z".
var Version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("pocket-nas", Version)
		return
	}
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "pocket-nas:", err)
		os.Exit(1)
	}
	if err := server.Start(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "pocket-nas:", err)
		os.Exit(1)
	}
}
