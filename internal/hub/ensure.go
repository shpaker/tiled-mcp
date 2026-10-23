package hub

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// Ensure makes sure a hub of this version listens on the port. A hub of
// another version is asked to stop; a missing hub is started from executable
// as a detached process, so it outlives the caller.
//
// The Tiled extension runs Ensure through a blocking Process.exec, so it must
// return quickly.
func Ensure(ctx context.Context, executable, version string, port int) error {
	client := NewClient(port)
	status, err := client.Status(ctx)
	switch {
	case err == nil && status.Version == version:
		return nil
	case err == nil:
		_ = client.Shutdown(ctx)
		if err := waitFor(ctx, 3*time.Second, func() bool {
			_, err := client.Status(ctx)
			return errors.Is(err, ErrNoHub)
		}); err != nil {
			return fmt.Errorf("hub %s did not stop: %w", status.Version, err)
		}
	case !errors.Is(err, ErrNoHub):
		return fmt.Errorf("port %d is taken by something else: %w", port, err)
	}

	command := exec.Command(executable, "hub", "serve", "--port", strconv.Itoa(port))
	detach(command)
	if err := command.Start(); err != nil {
		return err
	}
	_ = command.Process.Release()

	return waitFor(ctx, 5*time.Second, func() bool {
		status, err := client.Status(ctx)
		return err == nil && status.Version == version
	})
}

func waitFor(ctx context.Context, limit time.Duration, ready func() bool) error {
	deadline := time.Now().Add(limit)
	for !ready() {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", limit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return nil
}
