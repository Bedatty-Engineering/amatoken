#!/usr/bin/env bash
#
# amatoken dev update — local development
#
#   ./scripts/dev-update.sh
#   ./scripts/dev-update.sh -y
#
# Rebuilds the Docker image from local changes and restarts the container.
# Unlike update.sh, this does NOT pull from git — it uses your current working tree.
# Perfect for testing local modifications without committing.
#
# Existing data (SQLite volume, budgets, manual pricing, settings) is preserved.
#
# Flags:
#   -y, --yes        non-interactive (skip confirmation)
#   -d, --dir DIR    project directory (default: current dir)
#   -h, --help       show this help

set -euo pipefail

PROJECT_DIR="."
ASSUME_YES=0

c_red()   { printf '\033[31m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
c_blue()  { printf '\033[34m%s\033[0m' "$*"; }
c_yellow(){ printf '\033[33m%s\033[0m' "$*"; }

info() { echo "$(c_blue '==>') $*"; }
ok()   { echo "$(c_green '✓') $*"; }
warn() { echo "$(c_yellow '!') $*" >&2; }
die()  { echo "$(c_red '✗') $*" >&2; exit 1; }

usage() { sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    -y|--yes)    ASSUME_YES=1; shift ;;
    -d|--dir)    PROJECT_DIR="$2"; shift 2 ;;
    -h|--help)   usage ;;
    *)           die "unknown flag: $1 (use --help)" ;;
  esac
done

confirm() {
  [ "$ASSUME_YES" -eq 1 ] && return 0
  read -r -p "$1 [Y/n] " ans </dev/tty || return 1
  case "${ans:-Y}" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

need() { command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"; }

# --- pre-flight ----------------------------------------------------------
need docker

[ -d "$PROJECT_DIR" ] || die "directory not found: $PROJECT_DIR"
[ -f "$PROJECT_DIR/Dockerfile" ] || die "Dockerfile not found in $PROJECT_DIR"

if ! docker info >/dev/null 2>&1; then
  die "docker daemon not reachable"
fi

COMPOSE=""
if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

cd "$PROJECT_DIR"

# --- status ----------------------------------------------------------
info "Rebuilding from local changes"
if [ -d .git ]; then
  CURRENT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
  BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
  echo "  Branch: $BRANCH @ $CURRENT"
fi
echo "  Path: $(pwd)"
echo
confirm "Rebuild and restart container?" || die "aborted"
echo

# --- rebuild & restart ---------------------------------------------------
export AMATOKEN_UID="$(id -u)"
export AMATOKEN_GID="$(id -g)"

if [ -n "$COMPOSE" ]; then
  info "Rebuilding & restarting via $COMPOSE"
  $COMPOSE up --build -d
else
  warn "docker compose not found — falling back to plain docker"
  info "Rebuilding image"
  docker build -t amatoken .
  PORT=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "2002/tcp") 0).HostPort}}' amatoken 2>/dev/null || echo 2002)
  docker rm -f amatoken >/dev/null 2>&1 || true
  info "Starting container on port $PORT"
  docker run -d --name amatoken \
    --user "$(id -u):$(id -g)" \
    -p "${PORT}:2002" \
    -v "$HOME/.claude/projects:/claude-projects:ro" \
    -v "${HOME}/.local/share/rtk:/rtk-data:ro" \
    -v amatoken-db:/data \
    --restart unless-stopped \
    amatoken
fi

# --- health check --------------------------------------------------------
PORT=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "2002/tcp") 0).HostPort}}' amatoken 2>/dev/null || echo 2002)
URL="http://localhost:${PORT}"
info "Waiting for $URL/healthz"
for i in $(seq 1 30); do
  if curl -fsS "$URL/healthz" >/dev/null 2>&1; then
    ok "amatoken is up at $(c_green "$URL")"
    if command -v xdg-open >/dev/null 2>&1; then
      xdg-open "$URL" &>/dev/null &
    elif command -v open >/dev/null 2>&1; then
      open "$URL" &>/dev/null &
    fi
    exit 0
  fi
  sleep 1
done
warn "healthz did not respond after 30s — check logs: docker logs -f amatoken"
exit 1
