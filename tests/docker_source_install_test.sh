#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cp "$repo/install.sh" "$repo/update.sh" "$tmp/"
printf 'POSTGRES_PASSWORD=test-only\nARTEX_TAG=untrusted-tag\n' > "$tmp/.env"
mkdir "$tmp/bin"
cat > "$tmp/bin/docker" <<'MOCK'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$DOCKER_LOG"
MOCK
chmod +x "$tmp/bin/docker"

log="$tmp/docker.log"
DOCKER_LOG="$log" PATH="$tmp/bin:$PATH" bash "$tmp/install.sh" <<< '1'
printf 'compose version\ncompose up --build -d\n' > "$tmp/expected"
diff -u "$tmp/expected" "$log"

: > "$log"
DOCKER_LOG="$log" PATH="$tmp/bin:$PATH" bash "$tmp/update.sh" <<< '1'
printf 'compose version\ncompose up --build -d artex\n' > "$tmp/expected"
diff -u "$tmp/expected" "$log"
