#!/usr/bin/env bash
#
# amatoken uninstall
#
#   curl -fsSL https://raw.githubusercontent.com/Bedatty-Engineering/amatoken/main/scripts/uninstall.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/Bedatty-Engineering/amatoken/main/scripts/uninstall.sh | bash -s -- -y --purge
#
# Stops and removes the amatoken container and image. Optional flags wipe the
# SQLite volume (budgets / manual pricing / settings) and the cloned source dir.
#
# Flags:
#   -y, --yes        non-interactive (use ALL defaults — does NOT imply --purge)
#   -d, --dir DIR    install dir (default: $HOME/.amatoken)
#       --purge      also delete the SQLite volume AND the install dir
#       --keep-data  keep the SQLite volume (default in -y mode)
#       --keep-dir   keep the install dir   (default in -y mode)
#   -h, --help       show this help

set -euo pipefail

INSTALL_DIR="${AMATOKEN_DIR:-$HOME/.amatoken}"
ASSUME_YES=0
PURGE_VOLUME=""   # "" = ask / 1 = yes / 0 = no
PURGE_DIR=""      # same

c_red()   { printf '\033[31m%s\033[0m' "$*"; }
c_green() { printf '\033[32m%s\033[0m' "$*"; }
c_blue()  { printf '\033[34m%s\033[0m' "$*"; }

info() { echo "$(c_blue '==>') $*"; }
ok()   { echo "$(c_green '✓') $*"; }
warn() { echo "$(c_red '!') $*" >&2; }
die()  { warn "$*"; exit 1; }

usage() { sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [ $# -gt 0 ]; do
  case "$1" in
    -y|--yes)    ASSUME_YES=1; shift ;;
    -d|--dir)    INSTALL_DIR="$2"; shift 2 ;;
    --purge)     PURGE_VOLUME=1; PURGE_DIR=1; shift ;;
    --keep-data) PURGE_VOLUME=0; shift ;;
    --keep-dir)  PURGE_DIR=0; shift ;;
    -h|--help)   usage ;;
    *)           die "unknown flag: $1 (use --help)" ;;
  esac
done

confirm() {
  [ "$ASSUME_YES" -eq 1 ] && return 1   # in -y mode, default to NO for destructive prompts
  read -r -p "$1 [y/N] " ans </dev/tty || return 1
  case "${ans:-N}" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}

need() { command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"; }
need docker

if ! docker info >/dev/null 2>&1; then
  die "docker daemon not reachable."
fi

COMPOSE=""
if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

# --- stop & remove container --------------------------------------------
if [ -d "$INSTALL_DIR" ] && [ -n "$COMPOSE" ] && [ -f "$INSTALL_DIR/docker-compose.yml" ]; then
  info "Stopping container via $COMPOSE"
  ( cd "$INSTALL_DIR" && $COMPOSE down ) || true
elif docker ps -a --format '{{.Names}}' | grep -qx amatoken; then
  info "Stopping & removing 'amatoken' container"
  docker rm -f amatoken >/dev/null
fi
ok "Container removed"

# --- remove image --------------------------------------------------------
if docker image inspect amatoken >/dev/null 2>&1; then
  info "Removing 'amatoken' image"
  docker rmi amatoken >/dev/null 2>&1 || warn "could not remove image (still in use?)"
fi
# Compose-built image is named '<dir>-amatoken' or '<dir>_amatoken'; clean those too.
docker images --format '{{.Repository}}' | grep -E '(^|[/_-])amatoken$' | sort -u | while read -r img; do
  [ "$img" = "amatoken" ] && continue
  docker rmi "$img" >/dev/null 2>&1 || true
done
ok "Image(s) removed"

# --- volume --------------------------------------------------------------
if docker volume inspect amatoken-db >/dev/null 2>&1; then
  if [ -z "$PURGE_VOLUME" ]; then
    if confirm "Delete SQLite volume 'amatoken-db'? This wipes budgets, manual pricing and settings."; then
      PURGE_VOLUME=1
    else
      PURGE_VOLUME=0
    fi
  fi
  if [ "$PURGE_VOLUME" = "1" ]; then
    docker volume rm amatoken-db >/dev/null
    ok "Volume 'amatoken-db' deleted"
  else
    info "Keeping volume 'amatoken-db' (re-install will pick it up automatically)"
  fi
else
  info "No 'amatoken-db' volume found — skipping"
fi

# Compose may also create a project-namespaced volume like '<dir>_amatoken-db'.
docker volume ls --format '{{.Name}}' | grep -E '_amatoken-db$' | while read -r vol; do
  if [ -z "$PURGE_VOLUME" ] || [ "$PURGE_VOLUME" = "1" ]; then
    docker volume rm "$vol" >/dev/null 2>&1 && ok "Volume '$vol' deleted" || true
  fi
done

# --- install dir ---------------------------------------------------------
if [ -d "$INSTALL_DIR" ]; then
  if [ -z "$PURGE_DIR" ]; then
    if confirm "Delete install dir '$INSTALL_DIR'?"; then
      PURGE_DIR=1
    else
      PURGE_DIR=0
    fi
  fi
  if [ "$PURGE_DIR" = "1" ]; then
    rm -rf -- "$INSTALL_DIR"
    ok "Removed $INSTALL_DIR"
  else
    info "Keeping $INSTALL_DIR"
  fi
fi

echo
ok "amatoken uninstalled."
