package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLauncher runs this system's launcher against a release built here: it
// must download the archive, check its sha256, unpack the binary into the
// cache and start it with standard input and output left to the agent.
func TestLauncher(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not in PATH")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(data))

	name := "tiled-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	work := t.TempDir()
	binary := filepath.Join(work, name)
	build := exec.Command(goTool, "build", "-o", binary, "-ldflags", "-X main.version="+version, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	release := filepath.Join(work, "release")
	archive := fmt.Sprintf("tiled-mcp_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	sum := writeArchive(t, filepath.Join(release, archive), binary, name)
	checksums := fmt.Sprintf("%s  %s\n", sum, archive)
	if err := os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatal(err)
	}

	// A space and parentheses in the path, as in C:\Program Files (x86).
	cache := filepath.Join(work, "cache (x86)")
	url := filepath.ToSlash(release)
	if !strings.HasPrefix(url, "/") {
		url = "/" + url
	}
	command := launcherCommand(root)
	command.Env = append(os.Environ(),
		"TILED_MCP_DEV=",
		"TILED_MCP_CACHE="+cache,
		"TILED_MCP_RELEASE_URL=file://"+url,
	)
	initialize(t, command)

	if _, err := os.Stat(filepath.Join(cache, version, name)); err != nil {
		t.Errorf("binary is not in the cache: %v", err)
	}
}

// writeArchive packs the binary as GoReleaser does and returns the archive's
// sha256.
func writeArchive(t *testing.T, path, binary, name string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	compressed := gzip.NewWriter(io.MultiWriter(file, hash))
	archive := tar.NewWriter(compressed)
	header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), ModTime: time.Now()}
	if err := archive.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(content); err != nil {
		t.Fatal(err)
	}
	for _, closer := range []io.Closer{archive, compressed, file} {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// initialize sends MCP initialize through the launcher and expects the
// server's reply as the first line of standard output.
func initialize(t *testing.T, command *exec.Cmd) {
	t.Helper()
	var stderr strings.Builder
	command.Stderr = &stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(2*time.Minute, func() { _ = command.Process.Kill() })
	defer timer.Stop()

	request := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}` + "\n"
	if _, err := io.WriteString(stdin, request); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(stdout).ReadString('\n')
	_ = stdin.Close()
	waitErr := command.Wait()
	if err != nil {
		t.Fatalf("no reply (%v, exit: %v); stderr:\n%s", err, waitErr, stderr.String())
	}
	if !strings.Contains(reply, `"serverInfo"`) {
		t.Fatalf("reply is not an initialize result: %s\nstderr:\n%s", reply, stderr.String())
	}
}
