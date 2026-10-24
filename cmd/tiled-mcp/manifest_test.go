package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The launcher downloads the release named by VERSION, so every manifest must
// announce the same version.
func TestManifestsMatchVersion(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(data))
	for _, manifest := range []string{".claude-plugin/plugin.json", "plugin.json", "qwen-extension.json"} {
		data, err := os.ReadFile(filepath.Join(root, manifest))
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatalf("%s: %v", manifest, err)
		}
		if fields.Version != version {
			t.Errorf("%s: version %q, VERSION says %q", manifest, fields.Version, version)
		}
		if fields.Name != "tiled" {
			t.Errorf("%s: name %q, want tiled", manifest, fields.Name)
		}
	}
}

// Manifests name bin/tiled-mcp: bash runs it on macOS and Linux, and on
// Windows agents find bin/tiled-mcp.cmd for it through PATHEXT.
func TestLaunchersExist(t *testing.T) {
	bin := filepath.Join("..", "..", "bin")
	for _, name := range []string{"tiled-mcp", "tiled-mcp.cmd", "download.ps1"} {
		if _, err := os.Stat(filepath.Join(bin, name)); err != nil {
			t.Error(err)
		}
	}
}

func TestLauncherIsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit")
	}
	info, err := os.Stat(filepath.Join("..", "..", "bin", "tiled-mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("bin/tiled-mcp is not executable")
	}
}

// bash fails on CRLF; .gitattributes keeps the launcher LF in every checkout.
func TestLauncherHasLFEndings(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "bin", "tiled-mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("\r")) {
		t.Fatal("bin/tiled-mcp has CRLF line endings")
	}
}
