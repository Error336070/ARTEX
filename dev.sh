#!/usr/bin/env bash
# Development mode: run backend (:8787), traffic proxy (:8788) and next dev frontend (:5173) together.
# The frontend proxies /api to the backend; Ctrl-C stops both.
#
# For a single binary with embedded frontend, see the single-binary section of README.
set -euo pipefail
cd "$(dirname "$0")"

# Stop all child processes in this process group (backend and frontend) on exit.
cleanup() { kill 0 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# Backend (regular go run, without embedded frontend); configure Worker concurrency in System settings.
go run ./cmd/artex -addr :8787 -proxy 127.0.0.1:8788 &

# Frontend hot reload (Vite/Next dev server, /api proxied to :8787).
( cd web && npm run dev ) &

echo "[dev] Backend :8787 / Proxy :8788 / Frontend http://localhost:5173  (Ctrl-C to exit)"
wait
