package server

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/shpaker/tiled-mcp/extension"
)

// updateExtension rewrites an installed extension that an older tiled-mcp
// installed, or whose binary is gone from the cache. The user agreed to the
// extension when it was first installed, so updates need no new consent; a
// missing extension is left to tiled_install_extension, and a newer one is
// never downgraded.
func (s *Server) updateExtension() {
	config, _, err := extension.Installed()
	if err != nil {
		return
	}
	older, ok := olderVersion(config.Version, s.options.Version)
	if !ok {
		return
	}
	if !older {
		if config.Version != s.options.Version {
			return
		}
		if _, err := os.Stat(config.Binary); err == nil {
			return
		}
	}
	if _, err := extension.Install(extension.Config{
		Version: s.options.Version,
		Binary:  s.options.Binary,
		Port:    s.options.Port,
	}); err != nil {
		log.Printf("tiled-mcp: cannot update the Tiled extension: %v", err)
		return
	}
	log.Printf("tiled-mcp: updated the Tiled extension from %s to %s", config.Version, s.options.Version)
	s.mu.Lock()
	s.updatedFrom = config.Version
	s.mu.Unlock()
}

// olderVersion reports whether version a is older than b. ok is false when
// either is not X.Y.Z, as with development builds.
func olderVersion(a, b string) (older, ok bool) {
	x, okA := parseVersion(a)
	y, okB := parseVersion(b)
	if !okA || !okB {
		return false, false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i], true
		}
	}
	return false, true
}

func parseVersion(version string) ([3]int, bool) {
	var numbers [3]int
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != len(numbers) {
		return numbers, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return numbers, false
		}
		numbers[i] = n
	}
	return numbers, true
}
