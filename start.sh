#!/bin/sh
# ARTEX supervisor launcher (Linux / macOS / Docker ENTRYPOINT)
#
# Usage:
#   ./start.sh                       Run in foreground (Ctrl-C to stop)
#   nohup ./start.sh >artex.log 2>&1 &   Run in background
#   ./start.sh -addr :9000           Pass additional arguments unchanged to artex
#
# Starts artex and decides whether to restart it based on its exit code.
#
#   0      Normal user stop       → Exit loop
#   75     Restart requested      → Restart immediately (UI update or rollback)
#   Other  Crash                  → Restart after backoff (1→2→4... up to 60 seconds)
#
# Downloads, SHA256 verification and replacement are handled in Go rather than duplicated in sh and bat.
# A failed binary replacement would cause this script to restart the broken binary repeatedly,
# requiring manual recovery on the host. Verification and replacement belong to Go (selfupdate package),
# where artex performs them on startup, keeping this script simple.
set -u

cd "$(dirname "$0")" || exit 1

if [ -z "${ARTEX_SETUP_TOKEN:-}" ] && [ -f .setup-token ]; then
	ARTEX_SETUP_TOKEN="$(cat .setup-token)"
	export ARTEX_SETUP_TOKEN
fi

BIN=./artex
[ -x "$BIN" ] || { echo "[artex] Executable not found: $BIN" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0

# Forward stop signals to artex.
#
# Required in Docker: docker stop sends SIGTERM only to PID 1 (this script),
# not its children. Without forwarding, artex cannot shut down gracefully and receives SIGKILL
# after 10 seconds, interrupting running tasks.
forward() {
	stopping=1
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	# A signal interrupts wait with a return value >128 while the child is still shutting down gracefully.
	# Wait again to obtain its actual exit code.
	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[artex] Stopped"
		exit 0
	fi

	case "$code" in
		0)
			echo "[artex] Exited normally"
			exit 0
			;;
		"$RESTART_CODE")
			# Update/rollback ready: artex applies replacement on restart (see selfupdate.Bootstrap).
			echo "[artex] Restart requested (apply new version)..."
			delay=1
			;;
		*)
			echo "[artex] Abnormal exit (code=$code); restarting in ${delay}s" >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
