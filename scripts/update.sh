#!/usr/bin/env bash
#
# amatoken update
#
#   curl -fsSL https://raw.githubusercontent.com/Bedatty-Engineering/amatoken/main/scripts/update.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/Bedatty-Engineering/amatoken/main/scripts/update.sh | bash -s -- -y
#
# Pulls the latest code, rebuilds the image and restarts the container.
# Existing data (SQLite volume, budgets, manual pricing, settings) is preserved.
#
# Flags:
#   -y, --yes        non-interactive
#   -d, --dir DIR    install dir (default: $HOME/.amatoken)
#   -b, --branch B   git branch / ref (default: main)
#   -h, --help       show this help

set -euo pipefail

INSTALL_DIR="${AMATOKEN_DIR:-$HOME/.amatoken}"
BRANCH="${AMATOKEN_BRANCH:-main}"
ASSUME_YES=0

c_red()   { printf '\033[31m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
c_blue()  { printf '\033[34m%s\033[0m' "$*"; }

info() { echo "$(c_blue '==>') $*"; }
ok()   { echo "$(c_green '✓') $*"; }
warn() { echo "$(c_red '!') $*" >&2; }
die()  { warn "$*"; exit 1; }

usage() { sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    -y|--yes)    ASSUME_YES=1; shift ;;
    -d|--dir)    INSTALL_DIR="$2"; shift 2 ;;
    -b|--branch) BRANCH="$2"; shift 2 ;;
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
need git

[ -d "$INSTALL_DIR/.git" ] || die "$INSTALL_DIR is not an amatoken checkout. Did you run install.sh?"

if ! docker info >/dev/null 2>&1; then
  die "docker daemon not reachable."
fi

COMPOSE=""
if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

cd "$INSTALL_DIR"

# --- show what will change -----------------------------------------------
info "Fetching origin/$BRANCH"
git fetch --quiet origin "$BRANCH"

LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse "origin/$BRANCH")

if [ "$LOCAL" = "$REMOTE" ]; then
  ok "Already up to date ($LOCAL)"
  if ! confirm "Rebuild and restart anyway?"; then
    exit 0
  fi
else
  echo
  echo "Incoming commits:"
  git --no-pager log --oneline "${LOCAL}..${REMOTE}" | sed 's/^/  /'
  echo
  confirm "Apply update?" || die "aborted"
fi

# --- pull ----------------------------------------------------------------
info "Resetting to origin/$BRANCH"
git reset --hard "origin/$BRANCH"
ok "Source updated to $(git rev-parse --short HEAD)"

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
  PORT=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "2001/tcp") 0).HostPort}}' amatoken 2>/dev/null || echo 2001)
  docker rm -f amatoken >/dev/null 2>&1 || true
  info "Starting container on port $PORT"
  docker run -d --name amatoken \
    --user "$(id -u):$(id -g)" \
    -p "${PORT}:2001" \
    -v "$HOME/.claude/projects:/claude-projects:ro" \
    -v amatoken-db:/data \
    --restart unless-stopped \
    amatoken
fi

# --- health check --------------------------------------------------------
PORT=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "2001/tcp") 0).HostPort}}' amatoken 2>/dev/null || echo 2001)
URL="http://localhost:${PORT}"
info "Waiting for $URL/healthz"
for i in $(seq 1 30); do
  if curl -fsS "$URL/healthz" >/dev/null 2>&1; then
    ok "amatoken is up at $(c_green "$URL")"
    exit 0
  fi
  sleep 1
done
warn "healthz did not respond after 30s — check logs: docker logs -f amatoken"
exit 1
