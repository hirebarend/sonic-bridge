#!/usr/bin/env bash

set -Eeuo pipefail

readonly SCRIPT_NAME="${0##*/}"
readonly INSTALL_DIR="/opt/sonic-bridge"
readonly REPOSITORY="hirebarend/sonic-bridge"

IMAGE_TAG="latest"
ENVIRONMENT_FILE=""

usage() {
  cat <<USAGE
Deploy sonic-bridge on an Ubuntu DigitalOcean Droplet.

Usage:
  sudo ./$SCRIPT_NAME [--env-file PATH] [--tag TAG]

Options:
  --env-file PATH   Environment file to install as $INSTALL_DIR/.env.
                    Required on a first run; afterwards the existing file is
                    reused. Copy .env.example and fill it in.
  --tag TAG         Published image tag to deploy, for example v1.2.0.
                    Default: $IMAGE_TAG
  -h, --help        Show this help.

The image is pulled from ghcr.io/$REPOSITORY; nothing is compiled on the
Droplet. Re-run to move to a newer tag.

Before the first run, point DOMAIN's A record at this Droplet. Traefik obtains
the certificate over an HTTP-01 challenge, which fails while DNS still points
somewhere else.

Ports opened on the Droplet:
  80    Let's Encrypt challenge, redirected to 443
  443   the web interface, the listener WebSocket and the WAV stream
  9000  raw TCP source ingest for ESP32 firmware, plain TCP with no TLS

Once running, the relay keeps itself on the newest image published for the
deployed tag; the updater checks every UPDATE_INTERVAL seconds. Traefik never
updates on its own. Re-run this script only to change compose.yaml, .env, or
the image tag.

Example:
  sudo ./$SCRIPT_NAME --env-file /root/.env
USAGE
}

log() {
  printf '==> %s\n' "$*"
}

fatal() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_value() {
  [[ -n "${2-}" ]] || fatal "$1 requires a value"
}

compose() {
  docker compose \
    --project-directory "$INSTALL_DIR" \
    --env-file "$INSTALL_DIR/.env" "$@"
}

# Write KEY=VALUE into the installed env file, replacing any existing line.
# The updater reads that file on every pass, so it has to agree with what this
# script deployed. Passing IMAGE_TAG through the process environment instead
# would win over --env-file here but not there, and the updater would quietly
# move a pinned deployment back onto the tag recorded in .env.
set_env_var() {
  local key="$1" value="$2" file="$INSTALL_DIR/.env"

  if grep -q "^$key=" "$file"; then
    sed -i "s|^$key=.*|$key=$value|" "$file"
  else
    printf '%s=%s\n' "$key" "$value" >>"$file"
  fi

  chmod 0600 "$file"
}

install_docker() {
  local codename

  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    log "Using existing $(docker --version)"
    return
  fi

  log "Installing Docker Engine and Compose"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y ca-certificates curl

  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc

  # shellcheck source=/dev/null
  source /etc/os-release
  codename="${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}"
  [[ -n "$codename" ]] || fatal "cannot determine the Ubuntu codename"

  cat >/etc/apt/sources.list.d/docker.sources <<SOURCES
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $codename
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
SOURCES

  apt-get update
  apt-get install -y containerd.io docker-buildx-plugin docker-ce docker-ce-cli \
    docker-compose-plugin
}

fetch_compose() {
  # `latest` tracks the default branch; a release tag has a matching Git tag.
  local ref="$IMAGE_TAG"
  [[ "$ref" == "latest" ]] && ref="main"

  log "Fetching compose.yaml at $ref"
  install -d -m 0755 "$INSTALL_DIR"
  curl -fsSL "https://raw.githubusercontent.com/$REPOSITORY/$ref/compose.yaml" \
    -o "$INSTALL_DIR/compose.yaml"
}

install_env() {
  if [[ -n "$ENVIRONMENT_FILE" ]]; then
    log "Installing $ENVIRONMENT_FILE as $INSTALL_DIR/.env"
    install -m 0600 "$ENVIRONMENT_FILE" "$INSTALL_DIR/.env"
  fi

  [[ -f "$INSTALL_DIR/.env" ]] ||
    fatal "no $INSTALL_DIR/.env. Copy .env.example, fill it in, and pass it with --env-file."
}

configure_firewall() {
  if ! command -v ufw >/dev/null 2>&1; then
    log "ufw is not installed, leaving the firewall alone"
    return
  fi

  if ! ufw status | grep -q "Status: active"; then
    log "ufw is inactive, leaving the firewall alone"
    return
  fi

  local tcp_source_port
  tcp_source_port="$(grep -oP '^TCP_SOURCE_PORT=\K.*' "$INSTALL_DIR/.env" || true)"
  tcp_source_port="${tcp_source_port:-9000}"

  log "Allowing 80, 443 and $tcp_source_port through ufw"
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
  ufw allow "$tcp_source_port/tcp" >/dev/null
}

deploy() {
  log "Recording IMAGE_TAG=$IMAGE_TAG in $INSTALL_DIR/.env"
  set_env_var IMAGE_TAG "$IMAGE_TAG"

  log "Pulling the relay image at tag $IMAGE_TAG"
  compose pull

  log "Starting the stack"
  compose up -d --wait --wait-timeout 300
}

while (($# > 0)); do
  case "$1" in
    --env-file) require_value "$@"; ENVIRONMENT_FILE="$2"; shift 2 ;;
    --tag) require_value "$@"; IMAGE_TAG="$2"; shift 2 ;;
    -h | --help) usage; exit 0 ;;
    *) fatal "unknown option: $1" ;;
  esac
done

[[ -z "$ENVIRONMENT_FILE" || -r "$ENVIRONMENT_FILE" ]] ||
  fatal "cannot read --env-file: $ENVIRONMENT_FILE"
[[ $EUID -eq 0 ]] || fatal "run this script as root (for example, with sudo)"

install_docker
fetch_compose
install_env
configure_firewall
deploy

compose ps

domain="$(grep -oP '^DOMAIN=\K.*' "$INSTALL_DIR/.env" || true)"
log "Deployment complete. The interface is at https://${domain:-the DOMAIN set in $INSTALL_DIR/.env}"
