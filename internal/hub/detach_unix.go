//go:build !windows

package hub

import (
	"os/exec"
	"syscall"
)

// detach starts the command in its own session, without the caller's standard
// streams, so waiting for the caller does not wait for the hub.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
