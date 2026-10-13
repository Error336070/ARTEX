#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cp "$repo/install.sh" "$tmp/install.sh"
printf 'POSTGRES_PASSWORD=test-only\nARTEX_SETUP_TOKEN=\n' > "$tmp/.env"
mkdir "$tmp/bin"
cat > "$tmp/bin/docker" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$DOCKER_LOG"
printf '%s\n' "${ARTEX_SETUP_TOKEN-}" >> "$DOCKER_TOKEN_LOG"
MOCK
chmod +x "$tmp/bin/docker"

export DOCKER_LOG="$tmp/docker.log"
export DOCKER_TOKEN_LOG="$tmp/docker-token.log"
export PATH="$tmp/bin:$PATH"
ARTEX_SETUP_TOKEN=stale-from-host bash "$tmp/install.sh" <<< '1' > "$tmp/output"
token="$(sed -n 's/^ARTEX_SETUP_TOKEN=//p' "$tmp/.env")"
[[ "$token" =~ ^[a-f0-9]{64}$ ]]
[[ "$(grep -c '^ARTEX_SETUP_TOKEN=' "$tmp/.env")" -eq 1 ]]
grep -Fxq 'compose up --build -d' "$DOCKER_LOG"
[[ "$(tail -n 1 "$DOCKER_TOKEN_LOG")" == "$token" ]]

ARTEX_SETUP_TOKEN=stale-from-host bash "$tmp/install.sh" <<< '1' > "$tmp/output"
[[ "$(sed -n 's/^ARTEX_SETUP_TOKEN=//p' "$tmp/.env")" == "$token" ]]
[[ "$(grep -c '^ARTEX_SETUP_TOKEN=' "$tmp/.env")" -eq 1 ]]
[[ "$(tail -n 1 "$DOCKER_TOKEN_LOG")" == "$token" ]]

sed -i.bak 's/^ARTEX_SETUP_TOKEN=.*/ARTEX_SETUP_TOKEN=short/' "$tmp/.env"
rm -f "$tmp/.env.bak"
before="$(grep -c '^compose up' "$DOCKER_LOG")"
if bash "$tmp/install.sh" <<< '1' > "$tmp/output" 2>&1; then
  echo 'short setup token unexpectedly accepted' >&2
  exit 1
fi
[[ "$(grep -c '^compose up' "$DOCKER_LOG")" -eq "$before" ]]
