package server

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openTiled starts Tiled or hands the files to the running instance, which
// opens them (Tiled is a single-instance application).
func openTiled(files []string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", append([]string{"-a", "Tiled"}, files...)...)
	default:
		binary, err := findTiled()
		if err != nil {
			return err
		}
		command = exec.Command(binary, files...)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func findTiled() (string, error) {
	if path, err := exec.LookPath("tiled"); err == nil {
		return path, nil
	}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if root == "" {
				continue
			}
			path := filepath.Join(root, "Tiled", "tiled.exe")
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}
	return "", errors.New("tiled is not in PATH")
}
