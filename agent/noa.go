package agent

import (
	"log"
	"path/filepath"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/noaadapter"
)

// noaWarn returns a diagnostics sink tagging non-fatal noa messages with the
// session, routed through the package logger (agents have no per-instance one).
func noaWarn(session string) func(string) {
	return func(msg string) { log.Printf("[noa] %s: %s", session, msg) }
}

// noa is model-driven context compression introduced in norma v0.4.0, exposed as an experimental
// platform setting. It is mutually exclusive with built-in compaction. noaadapter.Enable installs
// its Compactor, Compress tool, and three persistent prompt blocks. Without Enable, noa stays off
// and built-in compaction remains active. Each agent reads its injected noaEnabledFn once per run,
// so changing the setting affects subsequent runs without rebuilding agents.

// enableNoa integrates noa into opts when enabled. archiveRoot is the persistent base directory
// for original compressed content: all agents use <workDir>/noa rather than task/intent directories.
// sessionID names a globally unique archive subdirectory, avoiding collisions within the base.
//
// Experimental integration failures must not interrupt tasks; report through onWarn and fall back to built-in compression.
// Clear opts.Compaction after successful integration to avoid agentcore warnings about two context managers.
func enableNoa(opts *agentcore.Options, enabled func() bool, archiveRoot, sessionID string, onWarn func(string)) {
	if enabled == nil || !enabled() {
		return
	}
	if opts.OnWarn == nil {
		opts.OnWarn = onWarn
	}
	if err := noaadapter.Enable(opts, noaadapter.Options{
		ArchiveBaseDir: filepath.Join(archiveRoot, "noa"),
		SessionID:      sessionID,
		OnWarn:         onWarn,
	}); err != nil {
		if onWarn != nil {
			onWarn("Failed to enable noa compression; falling back to built-in compression: " + err.Error())
		}
		return
	}
	// Compactor takes precedence over Compaction, but coexistence triggers warnings; clear it explicitly.
	opts.Compaction = nil
}
