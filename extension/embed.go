// Package extension holds the Tiled extension and installs it into Tiled's
// global extensions folder.
package extension

import (
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed tiled-mcp.js
var script []byte

// Name is the folder of the extension inside Tiled's extensions folder.
const Name = "tiled-mcp"

// Config is written next to the script; the script reads it on load.
type Config struct {
	Version string `json:"version"`
	Binary  string `json:"binary"`
	Port    int    `json:"port"`
}

// Dir returns Tiled's global extensions folder, or TILED_MCP_EXTENSIONS_DIR
// when set.
func Dir() (string, error) {
	if dir := os.Getenv("TILED_MCP_EXTENSIONS_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Preferences", "Tiled", "extensions"), nil
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return "", errors.New("LOCALAPPDATA is not set")
		}
		return filepath.Join(local, "Tiled", "extensions"), nil
	default:
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		return filepath.Join(config, "tiled", "extensions"), nil
	}
}

// Installed reads the config of the installed extension.
func Installed() (Config, string, error) {
	dir, err := Dir()
	if err != nil {
		return Config{}, "", err
	}
	path := filepath.Join(dir, Name)
	data, err := os.ReadFile(filepath.Join(path, "config.json"))
	if err != nil {
		return Config{}, path, err
	}
	var config Config
	err = json.Unmarshal(data, &config)
	return config, path, err
}

// Install writes the script and its config. Tiled watches the folder and
// reloads its scripts, so a running Tiled picks the extension up at once.
func Install(config Config) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, Name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	// The config goes first: the script reads it as soon as Tiled reloads.
	if err := os.WriteFile(filepath.Join(path, "config.json"), append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(path, "tiled-mcp.js"), script, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
