#!/usr/bin/env bash
# ARTEX updater: 1. Docker update (rebuild image from source)  2. Local update (rebuild binary)
# Complements install.sh: install performs initial deployment; update upgrades to a new version.
# No manual database migration is needed: artex reruns schema.sql idempotently on each startup (including ADD COLUMN/CREATE
# INDEX IF NOT EXISTS). Restarting performs migration. Data (pgdata volume, ./data, ./skills) is preserved.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }

# ── Optional: sync the repository to the latest code (updates Compose, scripts and local build source)───────
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "Not a Git working copy; skipping git pull"; return; }
  [ "$(ask 'Fetch the latest code (git pull --ff-only)? (y/n)' y)" = y ] || return
  if ! git pull --ff-only; then
    warn "git pull could not fast-forward (local changes or diverged branches); resolve manually and retry. Using current code for this run."
  fi
}

# ── 1. Docker update ───────────────────────────────
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "docker / docker compose not found; deploy with ./install.sh first"
  [ -f .env ] || die ".env not found; run ./install.sh for initial deployment first"

  # Rebuild only artex; postgres keeps its existing container and volume.
  info "Rebuilding the image from current source and starting (artex migrates the schema on restart)..."
  docker compose up --build -d artex
  ok "Update complete → http://localhost:8787"
  info "View logs:docker compose logs -f artex"
  info "Remove old images (optional):docker image prune -f"
}

# ── 2. Local build update ──────────────────────────────
update_local(){
  command -v go >/dev/null 2>&1 || die "Go (>=1.26) not found:https://go.dev/dl/"
  [ -f config.json ] || warn "config.json not found; use ./install.sh for initial deployment"
  ok "Go: $(go version)"

  if command -v npm >/dev/null 2>&1; then
    info "Rebuilding static frontend output..."
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "Rebuilding the single binary with embedded frontend..."
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
  else
    warn "npm not found: building the backend **without the embedded frontend** (run npm run dev separately for the frontend)"
    CGO_ENABLED=0 go build -o artex ./cmd/artex
  fi
  ok "Build complete → ./artex"
  warn "Restart the running artex process to apply changes (schema migrates automatically on restart)"
}

echo "=============================="
echo "  ARTEX update"
echo "  1) Docker update (rebuild image from source)"
echo "  2) Local update (rebuild with Go)"
echo "=============================="
case "$(ask 'Select' 1)" in
  1) sync_repo; update_docker ;;
  2) sync_repo; update_local ;;
  *) die "Invalid selection" ;;
esac
