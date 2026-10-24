//go:build !windows

package main

import (
	"os/exec"
	"path/filepath"
)

// launcherCommand starts bin/tiled-mcp as agents do on macOS and Linux.
func launcherCommand(root string) *exec.Cmd {
	return exec.Command(filepath.Join(root, "bin", "tiled-mcp"))
}
