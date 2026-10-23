package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestLauncherIsExecutable(t *testing.T) {
	info, err := os.Stat(filepath.Join("..", "..", "bin", "tiled-mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("bin/tiled-mcp is not executable")
	}
}
