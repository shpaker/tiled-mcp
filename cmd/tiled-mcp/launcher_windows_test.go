//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// launcherCommand starts bin\tiled-mcp.cmd with the command line cross-spawn,
// which the agents' MCP SDK uses, gives cmd.exe for a batch file.
func launcherCommand(root string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}
	launcher := filepath.Join(root, "bin", "tiled-mcp.cmd")
	command := exec.Command(shell)
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c ""` + launcher + `""`}
	return command
}
