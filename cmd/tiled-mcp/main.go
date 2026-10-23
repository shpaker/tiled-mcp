// Command tiled-mcp lets AI agents work with the maps open in the Tiled map
// editor.
//
//	tiled-mcp               MCP server over stdio, started by the agent
//	tiled-mcp hub ensure    start the hub unless one of this version runs (used by the extension)
//	tiled-mcp hub serve     run the hub in the foreground
//	tiled-mcp version
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"

	"github.com/shpaker/tiled-mcp/internal/hub"
	"github.com/shpaker/tiled-mcp/internal/server"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tiled-mcp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if len(args) > 0 && args[0] == "version" {
		fmt.Println(version)
		return nil
	}
	if len(args) > 1 && args[0] == "hub" {
		port, err := portFlag(args[2:])
		if err != nil {
			return err
		}
		switch args[1] {
		case "ensure":
			binary, err := executable()
			if err != nil {
				return err
			}
			return hub.Ensure(ctx, binary, version, port)
		case "serve":
			return hub.New(version, port).Serve(ctx)
		}
		return fmt.Errorf("unknown hub command %q", args[1])
	}

	port, err := portFlag(args)
	if err != nil {
		return err
	}
	binary, err := executable()
	if err != nil {
		return err
	}
	return server.New(server.Options{Version: version, Port: port, Binary: binary}).Run(ctx)
}

// portFlag reads --port, defaulting to TILED_MCP_PORT or hub.DefaultPort.
func portFlag(args []string) (int, error) {
	port := hub.DefaultPort
	if value := os.Getenv("TILED_MCP_PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("TILED_MCP_PORT: %w", err)
		}
		port = parsed
	}
	flags := flag.NewFlagSet("tiled-mcp", flag.ContinueOnError)
	flags.IntVar(&port, "port", port, "loopback port of the hub")
	if err := flags.Parse(args); err != nil {
		return 0, err
	}
	return port, nil
}

// executable is the binary the extension starts the hub with: TILED_MCP_BINARY
// when set (useful with `go run`, whose binary is temporary), else this one.
func executable() (string, error) {
	if binary := os.Getenv("TILED_MCP_BINARY"); binary != "" {
		return filepath.Abs(binary)
	}
	binary, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(binary)
}
