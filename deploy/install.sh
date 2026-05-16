#!/usr/bin/env bash
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/REPLACE_ME/sonic-bridge.git}"
REPO_BRANCH="${REPO_BRANCH:-main}"
INSTALL_DIR="${INSTALL_DIR:-/opt/sonic-bridge}"

if [[ $EUID -ne 0 ]]; then
  echo "error: must run as root (try: sudo $0)" >&2
  exit 1
fi

log() { printf "\n==> %s\n" "$*"; }

prompt_if_unset() {
  local var="$1" message="$2"
  if [[ -z "${!var:-}" ]]; then
    read -rp "$message: " value
    printf -v "$var" '%s' "$value"
  fi
}

prompt_if_unset SONIC_DOMAIN "Domain (e.g. sonic.example.com)"
prompt_if_unset SONIC_ACME_EMAIL "Email for Let's Encrypt"

if [[ -z "$SONIC_DOMAIN" || -z "$SONIC_ACME_EMAIL" ]]; then
  echo "error: SONIC_DOMAIN and SONIC_ACME_EMAIL are required" >&2
  exit 1
fi

log "Installing base packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y curl ca-certificates git gnupg

if ! command -v docker >/dev/null 2>&1; then
  log "Installing Docker"
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  source /etc/os-release
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $VERSION_CODENAME stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
  systemctl enable --now docker
fi

log "Cloning or updating repo at $INSTALL_DIR (branch: $REPO_BRANCH)"
if [[ -d "$INSTALL_DIR/.git" ]]; then
  git -C "$INSTALL_DIR" fetch --all --prune
  git -C "$INSTALL_DIR" checkout "$REPO_BRANCH"
  git -C "$INSTALL_DIR" reset --hard "origin/$REPO_BRANCH"
else
  git clone --branch "$REPO_BRANCH" "$REPO_URL" "$INSTALL_DIR"
fi

log "Writing $INSTALL_DIR/.env"
cat >"$INSTALL_DIR/.env" <<EOF
SONIC_DOMAIN=$SONIC_DOMAIN
SONIC_ACME_EMAIL=$SONIC_ACME_EMAIL
EOF
chmod 640 "$INSTALL_DIR/.env"

log "Preparing acme storage"
mkdir -p "$INSTALL_DIR/deploy/traefik/acme"
touch "$INSTALL_DIR/deploy/traefik/acme/acme.json"
chmod 600 "$INSTALL_DIR/deploy/traefik/acme/acme.json"

log "Building and starting stack"
cd "$INSTALL_DIR"
docker compose pull traefik
docker compose up -d --build

log "Done. https://$SONIC_DOMAIN will be reachable once the cert is issued."
log "Useful commands:"
log "  cd $INSTALL_DIR && docker compose ps"
log "  cd $INSTALL_DIR && docker compose logs -f traefik"
log "  cd $INSTALL_DIR && docker compose logs -f api"
