#!/usr/bin/env bash
# ARTEX installer: 1. All Docker  2. Build and run locally
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }
setup_token(){ head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# ── Docker detection / automatic installation ───────────────────
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "docker and docker compose found"; return
  fi
  warn "docker / docker compose not found"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask 'Install Docker automatically? (y/n)' y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "Docker installed (log in again for group changes to allow use without sudo)"
      else
        die "Install docker manually and retry"
      fi ;;
    Darwin) die "On macOS, install Docker Desktop:https://www.docker.com/products/docker-desktop/" ;;
    *)      die "Install docker manually and retry" ;;
  esac
}

# ── 1. All Docker ───────────────────────────────
install_docker(){
  ensure_docker
  if [ ! -f .env ]; then
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Postgres password (Enter generates a random value)' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY (optional; configure later in the UI)' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${pw}|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=${key}|" .env
    rm -f .env.bak
    ok ".env generated (POSTGRES_PASSWORD set)"
  else
    info "Using the existing .env"
  fi
  if ! grep -q '^ARTEX_SETUP_TOKEN=' .env; then
    printf '\nARTEX_SETUP_TOKEN=\n' >> .env
  fi
  local token
  token="$(sed -n 's/^ARTEX_SETUP_TOKEN=//p' .env | tail -n 1)"
  if [ -z "$token" ]; then
    token="$(setup_token)"
    sed -i.bak "s|^ARTEX_SETUP_TOKEN=.*|ARTEX_SETUP_TOKEN=${token}|" .env
    rm -f .env.bak
  fi
  [[ "$token" =~ ^[A-Za-z0-9]{32,}$ ]] || die "ARTEX_SETUP_TOKEN in .env must be at least 32 unquoted letters or digits"
  chmod 600 .env
  export ARTEX_SETUP_TOKEN="$token"
  info "Building the image from current source and starting..."
  docker compose up --build -d
  ok "Started → http://localhost:8787"
  info "Initial setup token: $token (only for first password setup at /setup)"
  info "View logs:docker compose logs -f artex"
}

# ── 2. Build and run locally ──────────────────────────────
install_local(){
  echo "Database installation mode:"
  echo "  1) Connect to an existing PostgreSQL instance"
  echo "  2) Start PostgreSQL in Docker (requires docker)"
  case "$(ask 'Select' 1)" in
    2)
      ensure_docker
      local pw; pw="$(ask 'Postgres password (Enter for random)' "$(rand)")"
      docker run -d --name artex-pg -p 5432:5432 \
        -e POSTGRES_USER=artex -e POSTGRES_PASSWORD="$pw" -e POSTGRES_DB=artex \
        -v artex-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=artex DB_PASS="$pw" DB_NAME=artex DB_SSL=disable ;;
    *)
      DB_HOST="$(ask 'Database host' 127.0.0.1)"
      DB_PORT="$(ask 'Port' 5432)"
      DB_USER="$(ask 'User' artex)"
      DB_PASS="$(ask 'Password' '')"
      DB_NAME="$(ask 'Database name' artex)"
      DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
  esac

  # Generate config.json
  cat > config.json <<JSON
{
  "database": {
    "host": "${DB_HOST}",
    "port": ${DB_PORT},
    "user": "${DB_USER}",
    "password": "${DB_PASS}",
    "dbname": "${DB_NAME}",
    "sslmode": "${DB_SSL}"
  }
}
JSON
  ok "config.json generated"

  # Check Go environment
  command -v go >/dev/null 2>&1 || die "Go not found; install Go (>=1.26) first:https://go.dev/dl/"
  ok "Go: $(go version)"

  # Embedding the frontend requires Node to generate static output
  if command -v npm >/dev/null 2>&1; then
    info "Building static frontend output..."
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "Building a single binary with embedded frontend..."
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
  else
    warn "npm not found: building the backend **without the embedded frontend** (run npm run dev separately for the frontend)"
    CGO_ENABLED=0 go build -o artex ./cmd/artex
  fi
  ok "Build complete → ./artex"

  if [ -z "${ARTEX_SETUP_TOKEN:-}" ]; then
    if [ -f .setup-token ]; then
      ARTEX_SETUP_TOKEN="$(cat .setup-token)"
    else
      ARTEX_SETUP_TOKEN="$(setup_token)"
    fi
  fi
  [ "${#ARTEX_SETUP_TOKEN}" -ge 32 ] || die "ARTEX_SETUP_TOKEN requires at least 32 characters"
  (umask 077; printf '%s\n' "$ARTEX_SETUP_TOKEN" > .setup-token)
  chmod 600 .setup-token
  export ARTEX_SETUP_TOKEN
  info "Initial setup token: $ARTEX_SETUP_TOKEN (only for first password setup at /setup)"
  info "Starting... (Ctrl-C to exit)"
  ./artex
}

echo "=============================="
echo "  ARTEX installation"
echo "  1) All Docker installation"
echo "  2) Run locally (Go build)"
echo "=============================="
case "$(ask 'Select' 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "Invalid selection" ;;
esac
