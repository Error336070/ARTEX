package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// smokeEnv lets the smoke-test child process skip Bootstrap.
// Without this guard, os.Executable() would be artex.new and derived paths
// would also have the .new prefix, avoiding actual upgrade files by accident.
// An explicit short circuit is clearer and avoids unnecessary disk probing.
const smokeEnv = "ARTEX_SELFUPDATE_SMOKE"

// Action is the instruction Bootstrap gives main.
type Action int

const (
	// Continue starts the server normally.
	Continue Action = iota
	// Restart exits immediately with ExitRestart so the supervisor relaunches it.
	Restart
)

// State describes upgrade status at startup so /api/update/check can tell the
// frontend whether the previous upgrade succeeded or was rolled back.
type State struct {
	Pending     bool   // Replacement pending stability confirmation
	RolledBack  bool   // Automatic rollback performed during this startup
	FailedStage bool   // Staged file discarded after verification/smoke-test failure
	Detail      string // One-line user-facing explanation
}

// Bootstrap runs at the start of main, before opening ports or databases.
// Three cases:
// 1. artex.new exists: verify and smoke test; replace and request restart on
//    success, otherwise discard it and continue with the current version.
// 2. Only the marker remains: count a startup attempt after replacement;
//    roll back after enough consecutive failures.
// 3. Neither exists: normal startup.
func Bootstrap() (Action, State) {
	if os.Getenv(smokeEnv) != "" {
		return Continue, State{}
	}
	p, err := ResolvePaths()
	if err != nil {
		log.Printf("[update] Bootstrap skipped: %v", err)
		return Continue, State{}
	}

	if _, err := os.Stat(p.New); err == nil {
		return applyStaged(p)
	}

	m, ok := readMarker(p.Marker)
	if !ok {
		return Continue, State{}
	}
	return confirmOrRollback(p, m)
}

// applyStaged verifies and replaces a staged version, or discards it on failure.
// This is the only point in the upgrade flow that overwrites the executable.
// The smoke test catches corrupt downloads, wrong architectures and missing
// dynamic libraries. If a binary cannot execute, Go code never runs and cannot
// roll back, while the supervisor keeps trying to restart it.
func applyStaged(p Paths) (Action, State) {
	m, _ := readMarker(p.Marker)

	if err := verifyStaged(p); err != nil {
		log.Printf("[update] Staged version failed verification and was discarded; continuing current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "New version verification failed; discarded: " + err.Error()}
	}

	if err := swap(p); err != nil {
		log.Printf("[update] Replacement failed; continuing current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "Replacement failed: " + err.Error()}
	}

	// Replacement succeeded. Keep the marker so the next startup, running the
	// new version, can confirm stability.
	m.Attempts = 0
	if m.StagedAt == 0 {
		m.StagedAt = time.Now().Unix()
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] Failed to write upgrade marker (automatic rollback unavailable): %v", err)
	}
	log.Printf("[update] Replaced with %s; exiting to restart (exit %d)", orUnknown(m.To), ExitRestart)
	return Restart, State{Pending: true}
}

// confirmOrRollback counts startup attempts after replacement and restores
// the previous version when the limit is exceeded.
// The counter increments only once Go code runs, covering initialization
// crashes such as incompatible configuration, busy ports or failed database
// migrations. The smoke test catches binaries that cannot execute at all.
func confirmOrRollback(p Paths, m marker) (Action, State) {
	m.Attempts++
	if m.Attempts > maxAttempts {
		if err := rollback(p); err != nil {
			// Stop requesting restarts if rollback fails to avoid an infinite loop.
			// Clear the marker and start in the current state; startup logs can explain
			// any remaining failure.
			log.Printf("[update] New version failed to start %d consecutive times; rollback also failed: %v", maxAttempts, err)
			_ = os.Remove(p.Marker)
			return Continue, State{Detail: "New version startup and rollback failed: " + err.Error()}
		}
		log.Printf("[update] New version failed to start %d consecutive times; rolled back to %s; exiting to restart (exit %d)",
			maxAttempts, orUnknown(m.From), ExitRestart)
		_ = os.Remove(p.Marker)
		return Restart, State{RolledBack: true, Detail: fmt.Sprintf("New version failed to start; rolled back to %s", orUnknown(m.From))}
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] Failed to update upgrade marker: %v", err)
	}
	log.Printf("[update] New version starting (attempt %d/%d); upgrade will be confirmed after stable operation",
		m.Attempts, maxAttempts)
	return Continue, State{Pending: true}
}

// Settle confirms stable operation of the new version and clears the marker.
// main calls it after a delay following HTTP listener startup. If the process
// does not survive that interval, the marker remains and subsequent startups
// continue counting attempts until rollback.
func Settle() {
	p, err := ResolvePaths()
	if err != nil {
		return
	}
	settle(p)
}

func settle(p Paths) {
	if _, ok := readMarker(p.Marker); !ok {
		return // Not an upgrade startup; nothing to do
	}
	if err := os.Remove(p.Marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[update] Failed to remove upgrade marker: %v", err)
		return
	}
	log.Printf("[update] New version stable; upgrade complete (previous version retained at %s)", p.Old)
}

// SettleDelay is the runtime needed to consider the new version stable.
const SettleDelay = 30 * time.Second

// verifyStaged checks SHA256, then actually runs the staged binary once.
func verifyStaged(p Paths) error {
	want, err := os.ReadFile(p.Sum)
	if err != nil {
		return fmt.Errorf("Read checksum: %w", err)
	}
	got, err := fileSHA256(p.New)
	if err != nil {
		return fmt.Errorf("Calculate checksum: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(want)), got) {
		return errors.New("SHA256 mismatch (download corrupted or tampered with)")
	}
	return smokeTest(p.New)
}

// smokeTest starts the new binary with -h to verify it can execute on this
// system. It catches truncated downloads, exec format errors and missing
// dependencies.
func smokeTest(bin string) error {
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("Set executable permission: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-h")
	cmd.Env = append(os.Environ(), smokeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("Smoke test timed out (new binary unresponsive)")
	}
	if err != nil {
		snippet := strings.TrimSpace(string(out))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return fmt.Errorf("Smoke test failed: %v: %s", err, snippet)
	}
	return nil
}

// swap replaces the current executable with the staged version.
// Unix and Windows allow renaming a running executable; Windows disallows
// deletion and overwriting, but not rename. No platform split or prior stop
// is needed here.
func swap(p Paths) error {
	// Windows rename does not overwrite an existing destination; remove the
	// .old file left by the previous upgrade first.
	if err := os.Remove(p.Old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Remove old backup %s: %w", p.Old, err)
	}
	if err := os.Rename(p.Current, p.Old); err != nil {
		return fmt.Errorf("Back up current version: %w", err)
	}
	if err := os.Rename(p.New, p.Current); err != nil {
		// Replacement failed after moving the current version aside. Restore it
		// exactly, or the next startup will have no executable.
		if rerr := os.Rename(p.Old, p.Current); rerr != nil {
			return fmt.Errorf("Failed to install new version (%v) and restore current version: %w", err, rerr)
		}
		return fmt.Errorf("Install new version: %w", err)
	}
	_ = os.Remove(p.Sum)
	return nil
}

// rollback restores the previous version backed up by swap.
func rollback(p Paths) error {
	if _, err := os.Stat(p.Old); err != nil {
		return fmt.Errorf("No backup available for rollback %s: %w", p.Old, err)
	}
	// Move the failed version to .failed for diagnosis rather than deleting it.
	failed := p.Current + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(p.Current, failed); err != nil {
		return fmt.Errorf("Move failed version aside: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		return fmt.Errorf("Restore previous version: %w", err)
	}
	return nil
}

// Rollback implements /api/update/rollback by restoring the previous version.
// It only replaces files; the supervisor restarts the process after the caller
// exits with ExitRestart.
func Rollback() error {
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Old); err != nil {
		return errors.New("No previous version available for rollback (" + p.Old + " does not exist)")
	}
	cleanStaged(p)
	if err := smokeTest(p.Old); err != nil {
		return fmt.Errorf("Previous version cannot run; rollback refused: %w", err)
	}
	// Swap current and backup versions so rollback can be reversed later.
	tmp := p.Current + ".swap"
	_ = os.Remove(tmp)
	if err := os.Rename(p.Current, tmp); err != nil {
		return fmt.Errorf("Move current version aside: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		_ = os.Rename(tmp, p.Current)
		return fmt.Errorf("Install previous version: %w", err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		log.Printf("[update] Failed to tidy backup after rollback (operation unaffected): %v", err)
	}
	_ = os.Remove(p.Marker)
	return nil
}

// HasBackup reports whether a previous version is available for rollback,
// allowing the frontend to decide whether to show the rollback button.
func HasBackup() bool {
	p, err := ResolvePaths()
	if err != nil {
		return false
	}
	_, err = os.Stat(p.Old)
	return err == nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Unknown version"
	}
	return s
}
