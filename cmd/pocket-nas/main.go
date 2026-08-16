// PocketNAS entry point: parses flags and starts the server.
package main

import (
	"fmt"
	"os"

	"pocket-nas/internal/config"
	"pocket-nas/internal/server"
)

func main() {
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
