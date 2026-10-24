package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shpaker/tiled-mcp/extension"
)

func TestOlderVersion(t *testing.T) {
	for _, c := range []struct {
		a, b      string
		older, ok bool
	}{
		{"0.1.0", "0.1.1", true, true},
		{"0.1.9", "0.1.10", true, true},
		{"0.9.9", "1.0.0", true, true},
		{"1.0.0", "0.9.9", false, true},
		{"0.1.1", "0.1.1", false, true},
		{"v0.1.0", "0.2.0", true, true},
		{"dev", "0.1.1", false, false},
		{"0.1.1", "dev", false, false},
		{"", "0.1.1", false, false},
		{"0.1", "0.1.1", false, false},
	} {
		older, ok := olderVersion(c.a, c.b)
		if older != c.older || ok != c.ok {
			t.Errorf("olderVersion(%q, %q) = %v, %v; want %v, %v", c.a, c.b, older, ok, c.older, c.ok)
		}
	}
}

func TestUpdateExtension(t *testing.T) {
	work := t.TempDir()
	binary := filepath.Join(work, "tiled-mcp")
	other := filepath.Join(work, "other-tiled-mcp")
	for _, path := range []string{binary, other} {
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(work, "gone", "tiled-mcp")

	for _, c := range []struct {
		name      string
		server    string
		installed *extension.Config
		updated   bool
	}{
		{"older", "0.2.0", &extension.Config{Version: "0.1.0", Binary: other}, true},
		{"newer", "0.2.0", &extension.Config{Version: "0.3.0", Binary: other}, false},
		{"same version, binary gone", "0.2.0", &extension.Config{Version: "0.2.0", Binary: missing}, true},
		{"same version, other binary", "0.2.0", &extension.Config{Version: "0.2.0", Binary: other}, false},
		{"not installed", "0.2.0", nil, false},
		{"dev server", "dev", &extension.Config{Version: "0.1.0", Binary: other}, false},
		{"dev extension", "0.2.0", &extension.Config{Version: "dev", Binary: missing}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TILED_MCP_EXTENSIONS_DIR", t.TempDir())
			if c.installed != nil {
				c.installed.Port = 1
				if _, err := extension.Install(*c.installed); err != nil {
					t.Fatal(err)
				}
			}
			s := New(Options{Version: c.server, Binary: binary, Port: freePort(t)})
			s.updateExtension()

			config, path, err := extension.Installed()
			if c.installed == nil {
				if err == nil {
					t.Fatal("installed an extension the user did not agree to")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := *c.installed
			from := ""
			if c.updated {
				want = extension.Config{Version: c.server, Binary: binary, Port: s.options.Port}
				from = c.installed.Version
				if _, err := os.Stat(filepath.Join(path, "tiled-mcp.js")); err != nil {
					t.Fatal(err)
				}
			}
			if config != want {
				t.Errorf("config = %+v, want %+v", config, want)
			}
			if s.updatedFrom != from {
				t.Errorf("updatedFrom = %q, want %q", s.updatedFrom, from)
			}
		})
	}
}

func TestStatusReportsUpdate(t *testing.T) {
	t.Setenv("TILED_MCP_EXTENSIONS_DIR", t.TempDir())
	binary := filepath.Join(t.TempDir(), "tiled-mcp")
	if err := os.WriteFile(binary, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := extension.Install(extension.Config{Version: "0.1.0", Binary: binary}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Version: "0.2.0", Binary: binary, Port: freePort(t)})
	result := s.status(context.Background())
	if !result.Extension.Current || result.Extension.Updated != "0.1.0 → 0.2.0" {
		t.Fatalf("extension = %+v", result.Extension)
	}
}

func TestStatusAdvisesPluginUpdateForNewerExtension(t *testing.T) {
	t.Setenv("TILED_MCP_EXTENSIONS_DIR", t.TempDir())
	if _, err := extension.Install(extension.Config{Version: "0.3.0", Binary: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Version: "0.2.0", Binary: "here", Port: freePort(t)})
	result := s.status(context.Background())
	if result.Extension.Version != "0.3.0" || !strings.Contains(result.Next, "update the plugin") {
		t.Fatalf("status = %+v, next %q", result.Extension, result.Next)
	}
}

// freePort returns a loopback port nothing listens on, so no hub answers.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}
