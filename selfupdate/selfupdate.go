// Package selfupdate implements one-click ARTEX updates: download a GitHub
// Release binary, verify and stage it, then replace it atomically on startup.
// Responsibilities (see start.sh / start.bat):
// - Startup scripts supervise the process and restart according to exit codes.
// - This package handles download, SHA256, smoke tests, replacement and rollback.
// Keeping verification in Go avoids duplicating sha256sum/shasum/certutil and
// smoke-test logic in sh and bat. A binary that cannot execute would otherwise
// restart indefinitely until manual recovery.
// An upgrade requires three process starts:
// 1. Old server receives /api/update/apply, downloads and verifies the package,
//    stages artex.new and exits with code 75.
// 2. Supervisor starts the old server; Bootstrap verifies and smoke tests
//    artex.new, replaces the executable and exits with code 75.
// 3. Supervisor starts the new server; Bootstrap counts an attempt and clears
//    the marker after successful startup.
// Failed verification discards staging and keeps the old version. Three
// consecutive failures before settling restore artex.old automatically.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExitRestart requests immediate relaunch by the supervisor (EX_TEMPFAIL),
// without crash backoff. Exit 0 stops the loop normally; other codes are crashes.
const ExitRestart = 75

// maxAttempts limits startup attempts after replacement. Each startup
// increments the count; surviving settleDelay clears the marker. Consecutive
// failures up to maxAttempts trigger automatic rollback.
const maxAttempts = 3

// Paths contains every upgrade file, all in the executable directory.
// CWD may be / or another location when running as a service; placing staging
// there would break replacement.
type Paths struct {
	Dir     string // Executable directory
	Current string // Current executable        artex      / artex.exe
	New     string // Staged version            artex.new  / artex.new.exe
	Sum     string // New version SHA256 (hex)  artex.new.sha256 / artex.new.exe.sha256
	Old     string // Previous version backup      artex.old  / artex.old.exe
	Marker  string // Upgrade state marker            artex.upgrade.json
}

// ResolvePaths derives upgrade paths from the current executable.
// On Windows, .new/.old must retain .exe for smoke tests and later execution.
// Remove the extension before composing names for consistent platform naming.
func ResolvePaths() (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("Locate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)
	ext := filepath.Ext(name) // ".exe" on Windows, usually empty on Unix
	stem := strings.TrimSuffix(name, ext)

	join := func(suffix string) string { return filepath.Join(dir, stem+suffix+ext) }
	return Paths{
		Dir:     dir,
		Current: exe,
		New:     join(".new"),
		Sum:     join(".new") + ".sha256",
		Old:     join(".old"),
		Marker:  filepath.Join(dir, stem+".upgrade.json"),
	}, nil
}

// marker records replacement progress to trigger rollback if the new version fails.
type marker struct {
	From     string `json:"from"`     // Version before upgrade
	To       string `json:"to"`       // Target version
	Attempts int    `json:"attempts"` // Startup attempts after replacement
	StagedAt int64  `json:"staged_at"`
}

func readMarker(path string) (marker, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return marker{}, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil {
		return marker{}, false
	}
	return m, true
}

func writeMarker(path string, m marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// cleanStaged removes staging after success, verification failure or
// cancellation, preventing leftover artex.new from being retried on startup.
func cleanStaged(p Paths) {
	_ = os.Remove(p.New)
	_ = os.Remove(p.Sum)
}

// CompareVersions returns -1/0/1 for a<b / a==b / a>b.
// ok=false means at least one version is incomparable, such as "dev" or
// "0.3.7-2-gabc1234-dirty" from git describe. Disable one-click updates in this
// case to avoid replacing development builds and uncommitted changes.
func CompareVersions(a, b string) (int, bool) {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range 3 {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parseVersion parses "v0.3.7" or "0.3.7" into [3]int.
// Only clean three-part versions are accepted. Non-tag builds from git describe,
// such as "0.3.7-2-gabc1234", must remain incomparable instead of being treated
// as 0.3.7, which could mark them current or overwrite them with a stable release.
func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// InDocker reports whether the process runs in a container. Replacement writes
// to the writable layer; recreating with `docker compose up -d` restores the
// image version. This is expected when pulling a new image, but the frontend
// should explain it.
func InDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}
