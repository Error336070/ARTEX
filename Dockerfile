# syntax=docker/dockerfile:1
#
# Multistage build: static frontend export → Go single binary (embedded frontend) → runtime image.
# The original Docker Hub image source (autumn27/artex) is unavailable; this repository supports building entirely from source:
#   docker compose build        # or docker build -t artex:local .
# buildx injects TARGETARCH (amd64/arm64); Go cross-compilation requires no QEMU.
# The runtime layer installs tools; all compilation happens in build stages.
# Override dependency mirrors for networks in China (defaults already support them):
#   --build-arg GOPROXY=https://goproxy.cn,direct
#   --build-arg NPM_REGISTRY=https://registry.npmmirror.com

########## Stage 1: static frontend export ##########
FROM node:20-bookworm AS web
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /web
# --ignore-scripts skips prepare (husky), which fails without .git in the container.
# Next.js/Biome platform binaries use optionalDependencies and do not rely on lifecycle scripts.
COPY web/package.json web/package-lock.json ./
RUN npm config set registry "$NPM_REGISTRY" && npm ci --ignore-scripts
COPY web/ ./
RUN NEXT_EXPORT=1 npm run build:static

########## Stage 2: Go backend (embedded frontend, static compilation)##########
FROM golang:1.26-bookworm AS gobuild
ARG TARGETARCH
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
WORKDIR /src
COPY go.mod go.sum ./
COPY norma/go.mod norma/go.sum ./norma/
RUN go mod download
COPY . .
# Copy static frontend output from the web stage (.dockerignore excludes host dist to avoid conflicts)
COPY --from=web /web/out/ server/webui/dist/
RUN CGO_ENABLED=0 GOARCH="${TARGETARCH}" go build -tags embedui -trimpath -o /out/artex ./cmd/artex

########## Stage 3: runtime image (as before: tools and preinstalled Playwright)##########
FROM python:3.12-slim-bookworm
ARG NPM_REGISTRY=https://registry.npmmirror.com
# Global npm packages and Chromium downloads also use mirrors in China (accessible worldwide)
ENV npm_config_registry=$NPM_REGISTRY \
    PLAYWRIGHT_DOWNLOAD_HOST=https://cdn.npmmirror.com/binaries/playwright
# Common tools: ripgrep / curl / vim, plus reconnaissance utilities (adjust as needed).
# Install Node 20.x from NodeSource: bookworm ships apt nodejs 18; Playwright requires >=20.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# Preinstall Playwright MCP and CLI globally to avoid npx downloads at runtime.
# @playwright/mcp: run browser MCP with `npx @playwright/mcp` (globally installed; no -y/@latest needed).
# @playwright/cli provides playwright-cli; verify with --help after installation.
# Install playwright for browser management, then use --with-deps to provision Chromium and its system dependencies,
# so MCP/CLI works on first start without downloading a browser.
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# Single binary from the build stage (embedded frontend)
COPY --from=gobuild /out/artex /app/artex
# Supervisor launcher: exit codes determine restarts; UI updates rely on it to apply replacements.
# It also forwards SIGTERM to artex: docker stop signals only PID 1,
# so without forwarding artex cannot shut down gracefully and receives SIGKILL after 10 seconds.
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# data/ persistence (SQLite + jwt.key)
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
