#!/usr/bin/env bash
#
# amatoken quick install
#
#   curl -fsSL https://raw.githubusercontent.com/Bedatty-Engineering/amatoken/main/scripts/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/Bedatty-Engineering/amatoken/main/scripts/install.sh | bash -s -- -y
#
# Flags:
#   -y, --yes        non-interactive (assume defaults, no prompts)
#   -d, --dir DIR    install dir (default: $HOME/.amatoken)
#   -p, --port PORT  host port to bind (default: 2002, prompted if interactive)
#   -b, --branch B   git branch / ref (default: main)
#   -h, --help       show this help

set -euo pipefail

REPO_URL="https://github.com/Bedatty-Engineering/amatoken.git"
INSTALL_DIR="${AMATOKEN_DIR:-$HOME/.amatoken}"
DEFAULT_PORT=2002
PORT="${AMATOKEN_PORT:-}"
BRANCH="${AMATOKEN_BRANCH:-main}"
ASSUME_YES=0
PORT_FROM_FLAG=0

c_red()   { printf '\033[31m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
c_blue()  { printf '\033[34m%s\033[0m' "$*"; }
c_dim()   { printf '\033[2m%s\033[0m' "$*"; }

info()  { echo "$(c_blue '==>') $*"; }
ok()    { echo "$(c_green '✓') $*"; }
warn()  { echo "$(c_red '!') $*" >&2; }
die()   { warn "$*"; exit 1; }

usage() { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    -y|--yes)    ASSUME_YES=1; shift ;;
    -d|--dir)    INSTALL_DIR="$2"; shift 2 ;;
    -p|--port)   PORT="$2"; PORT_FROM_FLAG=1; shift 2 ;;
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

valid_port() {
  case "$1" in ''|*[!0-9]*) return 1 ;; esac
  [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

# Resolve port: flag/env > interactive prompt > default.
if [ -z "$PORT" ]; then
  if [ "$ASSUME_YES" -eq 0 ] && [ -t 0 -o -e /dev/tty ]; then
    read -r -p "Host port to bind [${DEFAULT_PORT}]: " ans </dev/tty || ans=""
    PORT="${ans:-$DEFAULT_PORT}"
  else
    PORT="$DEFAULT_PORT"
  fi
fi
valid_port "$PORT" || die "invalid port: $PORT"

# --- pre-flight ----------------------------------------------------------
info "Checking prerequisites"
need docker
need git

if ! docker info >/dev/null 2>&1; then
  die "docker daemon not reachable. Start Docker (or add your user to the 'docker' group) and re-run."
fi

COMPOSE=""
if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi
[ -n "$COMPOSE" ] && ok "Using: $COMPOSE" || warn "docker compose not found — will fall back to 'docker run'"

if [ ! -d "$HOME/.claude/projects" ]; then
  warn "$HOME/.claude/projects does not exist yet — amatoken will start, but the dashboard stays empty until Claude Code logs at least one session."
fi

# --- fetch source --------------------------------------------------------
if [ -d "$INSTALL_DIR/.git" ]; then
  info "Updating existing checkout at $INSTALL_DIR"
  git -C "$INSTALL_DIR" fetch --depth 1 origin "$BRANCH"
  git -C "$INSTALL_DIR" reset --hard "origin/$BRANCH"
else
  if [ -e "$INSTALL_DIR" ]; then
    die "$INSTALL_DIR exists and is not a git checkout. Remove it or pass --dir."
  fi
  info "Cloning $REPO_URL → $INSTALL_DIR (branch: $BRANCH)"
  git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$INSTALL_DIR"
fi
ok "Source ready"

cd "$INSTALL_DIR"

# --- build & run ---------------------------------------------------------
# docker-compose.yml reads AMATOKEN_PORT, AMATOKEN_UID and AMATOKEN_GID.
# (UID is readonly in bash, so we use namespaced env vars instead.)
export AMATOKEN_PORT="$PORT"
export AMATOKEN_UID="$(id -u)"
export AMATOKEN_GID="$(id -g)"
info "Host port → $PORT (container listens on 2002)"

if [ -n "$COMPOSE" ]; then
  info "Building & starting via $COMPOSE"
  $COMPOSE up --build -d
else
  info "Building image (plain docker)"
  docker build -t amatoken .
  docker volume create amatoken-db >/dev/null
  docker rm -f amatoken >/dev/null 2>&1 || true
  info "Starting container"
  docker run -d --name amatoken \
    --user "$(id -u):$(id -g)" \
    -p "${PORT}:2002" \
    -v "$HOME/.claude/projects:/claude-projects:ro" \
    -v amatoken-db:/data \
    --restart unless-stopped \
    amatoken
fi

# --- health check --------------------------------------------------------
URL="http://localhost:${PORT}"
info "Waiting for $URL/healthz"
for i in $(seq 1 30); do
  if curl -fsS "$URL/healthz" >/dev/null 2>&1; then
    ok "amatoken is up at $(c_green "$URL")"
    break
  fi
  sleep 1
  [ "$i" -eq 30 ] && warn "healthz did not respond after 30s — check logs: docker logs -f amatoken"
done

# --- open browser --------------------------------------------------------
if [ "$ASSUME_YES" -eq 0 ] && confirm "Open $URL in your browser?"; then
  if command -v xdg-open >/dev/null 2>&1; then xdg-open "$URL" >/dev/null 2>&1 &
  elif command -v open >/dev/null 2>&1; then open "$URL" >/dev/null 2>&1 &
  fi
fi

cat <<EOF

$(c_green 'Done.') Quick reference:

  $(c_dim '# follow logs')
  cd $INSTALL_DIR && ${COMPOSE:-docker} logs -f${COMPOSE:+ }${COMPOSE:+}${COMPOSE:-amatoken}

  $(c_dim '# stop')
  cd $INSTALL_DIR && ${COMPOSE:-docker rm -f amatoken}${COMPOSE:+ down}

  $(c_dim '# update later')
  bash <(curl -fsSL https://raw.githubusercontent.com/Bedatty-Engineering/amatoken/main/scripts/install.sh) -y

EOF
