#!/usr/bin/env bash
# Deploy one production role of OutlookEmail. Run as root on Ubuntu 22.04+.
#
# Quick start: sudo bash deploy-production.sh primary
#              sudo bash deploy-production.sh replica
# Missing deployment values are requested interactively. All flags remain
# available for automation; run --help for the complete examples.
#
# The script deliberately prompts for secrets and never puts enrollment tokens
# or passwords in command-line arguments, shell history, or generated logs.
set -Eeuo pipefail
umask 077

APP_DIR="/opt/outlook-email"
ROLE="${1:-}"
shift || true
IMAGE=""
ADMIN_DOMAIN=""
QUERY_DOMAIN=""
ACME_EMAIL=""
MASTER_URL=""
NODE_ID=""
FINGERPRINT=""
BIND_IP=""
REGISTRY_USER=""
CADDY_IMAGE="${CADDY_IMAGE:-caddy:2.8-alpine}"
REPLICAS=()
GHCR_LOGGED_IN=0

usage() {
  sed -n '2,8p' "$0"
  exit 2
}

[[ "$ROLE" == -h || "$ROLE" == --help ]] && usage
[[ $EUID -eq 0 ]] || { echo "Run this script with sudo or as root." >&2; exit 1; }
[[ "$ROLE" == primary || "$ROLE" == replica ]] || usage

while [[ $# -gt 0 ]]; do
  case "$1" in
    --image) IMAGE="${2:?missing image}"; shift 2 ;;
    --admin-domain) ADMIN_DOMAIN="${2:?missing domain}"; shift 2 ;;
    --query-domain) QUERY_DOMAIN="${2:?missing domain}"; shift 2 ;;
    --acme-email) ACME_EMAIL="${2:?missing email}"; shift 2 ;;
    --master) MASTER_URL="${2:?missing master URL}"; shift 2 ;;
    --node-id) NODE_ID="${2:?missing node ID}"; shift 2 ;;
    --fingerprint) FINGERPRINT="${2:?missing fingerprint}"; shift 2 ;;
    --bind-ip) BIND_IP="${2:?missing bind IP}"; shift 2 ;;
    --registry-user) REGISTRY_USER="${2:?missing registry username}"; shift 2 ;;
    --replica) REPLICAS+=("${2:?missing replica address}"); shift 2 ;;
    -h|--help) usage ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

prompt_required() {
  local variable_name="$1" prompt_text="$2" response
  response="${!variable_name:-}"
  if [[ -z "$response" ]]; then
    read -r -p "$prompt_text: " response
    printf -v "$variable_name" '%s' "$response"
  fi
  [[ -n "${!variable_name}" ]] || { echo "$prompt_text is required." >&2; exit 2; }
}

prompt_optional() {
  local variable_name="$1" prompt_text="$2" response
  response="${!variable_name:-}"
  if [[ -z "$response" ]]; then
    read -r -p "$prompt_text (leave blank to skip): " response
    printf -v "$variable_name" '%s' "$response"
  fi
}

prompt_required IMAGE "Container image (immutable @sha256 reference)"
if [[ "$IMAGE" == ghcr.io/* ]]; then
  prompt_required REGISTRY_USER "GHCR username"
fi
if [[ "$ROLE" == primary ]]; then
  prompt_required ADMIN_DOMAIN "Primary administration domain"
  prompt_optional QUERY_DOMAIN "Public query domain"
  prompt_required ACME_EMAIL "ACME notification email"
  while [[ -n "$QUERY_DOMAIN" && ${#REPLICAS[@]} -lt 2 ]]; do
    next_replica=$(( ${#REPLICAS[@]} + 1 ))
    read -r -p "Replica $next_replica private/VPN IP: " replica_address
    [[ -n "$replica_address" ]] || { echo "A query gateway requires two replica addresses." >&2; exit 2; }
    REPLICAS+=("$replica_address")
  done
  if [[ -n "$QUERY_DOMAIN" && ${#REPLICAS[@]} -ne 2 ]]; then
    echo "Query gateway requires exactly two --replica private addresses." >&2; exit 2
  fi
else
  prompt_required MASTER_URL "Primary administration URL (https://...)"
  prompt_required NODE_ID "Node ID from primary"
  prompt_required FINGERPRINT "Primary fingerprint"
  prompt_required BIND_IP "This replica private/VPN IP"
fi

install_docker() {
  if command -v docker >/dev/null 2>&1; then return; fi
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y docker.io docker-compose-plugin || \
    DEBIAN_FRONTEND=noninteractive apt-get install -y docker.io docker-compose
  systemctl enable --now docker
}

compose() {
  if docker compose version >/dev/null 2>&1; then docker compose "$@"; else docker-compose "$@"; fi
}

random_secret() { openssl rand -hex 32; }
cleanup_registry_login() {
  if [[ "$GHCR_LOGGED_IN" -eq 1 ]]; then
    docker logout ghcr.io >/dev/null 2>&1 || true
  fi
}
trap cleanup_registry_login EXIT
install_docker

if [[ "$IMAGE" == ghcr.io/* && -n "$REGISTRY_USER" ]]; then
  read -r -s -p "Paste the GHCR read token for $REGISTRY_USER: " GHCR_TOKEN; echo
  [[ -n "$GHCR_TOKEN" ]] || { echo "A GHCR token is required." >&2; exit 2; }
  printf '%s' "$GHCR_TOKEN" | docker login ghcr.io --username "$REGISTRY_USER" --password-stdin
  unset GHCR_TOKEN
  GHCR_LOGGED_IN=1
fi

install -d -m 700 "$APP_DIR/data" "$APP_DIR/backups"
cd "$APP_DIR"

if [[ ! -f .env ]]; then
  if [[ "$ROLE" == primary ]]; then
    read -r -s -p "Create the primary admin password: " LOGIN_PASSWORD; echo
    [[ ${#LOGIN_PASSWORD} -ge 12 ]] || { echo "Use at least 12 characters." >&2; exit 2; }
    cat > .env <<EOF
NODE_ROLE=primary
SECRET_KEY=$(random_secret)
LOGIN_PASSWORD=$LOGIN_PASSWORD
DATABASE_PATH=/app/data/outlook_accounts.db
FLASK_ENV=production
DOCKER_UPDATE_ENABLED=false
GUNICORN_THREADS=4
EOF
  else
    cat > .env <<EOF
NODE_ROLE=replica
SECRET_KEY=$(random_secret)
MASTER_URL=$MASTER_URL
DATABASE_PATH=/app/data/outlook_accounts.db
FLASK_ENV=production
DOCKER_UPDATE_ENABLED=false
GUNICORN_THREADS=4
REPLICA_POLL_SECONDS=10
REPLICA_MAX_STALE_SECONDS=86400
REPLICATION_EVENT_RETENTION_DAYS=30
BIND_IP=$BIND_IP
EOF
  fi
  chmod 600 .env
else
  echo "Reusing existing $APP_DIR/.env; its SECRET_KEY remains unchanged."
fi

if [[ "$ROLE" == replica ]]; then
  cat > compose.yml <<EOF
services:
  email:
    image: $IMAGE
    container_name: outlook-email-replica
    restart: unless-stopped
    env_file: .env
    ports:
      - "\${BIND_IP}:5000:5000"
    volumes:
      - ./data:/app/data
    healthcheck:
      test: ["CMD", "python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:5000/health/ready', timeout=5)"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 30s
    logging:
      driver: json-file
      options: {max-size: 20m, max-file: "5"}
EOF
  docker pull "$IMAGE"
  if [[ ! -f data/cluster/identity.db ]]; then
    read -r -s -p "Paste the one-time enrollment token for $NODE_ID: " ENROLLMENT_TOKEN; echo
    [[ -n "$ENROLLMENT_TOKEN" ]] || { echo "Enrollment token is required." >&2; exit 2; }
    printf '%s\n' "$ENROLLMENT_TOKEN" | compose run --rm -T email \
      python -m outlook_web.cluster.cli enroll \
      --master "$MASTER_URL" --node-id "$NODE_ID" \
      --master-fingerprint "$FINGERPRINT" --identity-dir /app/data/cluster
    unset ENROLLMENT_TOKEN
  fi
  compose up -d
  echo "Replica deployed. Wait for: curl -f http://$BIND_IP:5000/health/ready"
  exit 0
fi

cat > compose.yml <<EOF
services:
  email:
    image: $IMAGE
    container_name: outlook-email-primary
    restart: unless-stopped
    env_file: .env
    volumes:
      - ./data:/app/data
    expose: ["5000"]
    healthcheck:
      test: ["CMD", "python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:5000/health/ready', timeout=5)"]
      interval: 15s
      timeout: 5s
      retries: 3
      start_period: 30s
    logging:
      driver: json-file
      options: {max-size: 20m, max-file: "5"}
  caddy:
    image: $CADDY_IMAGE
    container_name: outlook-email-caddy
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./caddy-data:/data
      - ./caddy-config:/config
    depends_on: [email]
EOF

if [[ -n "$QUERY_DOMAIN" ]]; then
  cat >> compose.yml <<EOF
  query-gateway:
    image: haproxy:3.0-alpine
    container_name: outlook-email-query-gateway
    restart: unless-stopped
    volumes:
      - ./haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro
    depends_on: [email]
EOF
  cat > haproxy.cfg <<EOF
global
  log stdout format raw local0
defaults
  mode http
  timeout connect 5s
  timeout client 120s
  timeout server 120s
frontend query_in
  bind :8000
  default_backend replicas
backend replicas
  balance leastconn
  option httpchk
  http-check send meth GET uri /health/ready ver HTTP/1.1 hdr Host localhost
  http-check expect status 200
  http-response set-header X-Mail-Node %[srv_name]
  server replica-a ${REPLICAS[0]}:5000 check inter 3s fall 2 rise 2
  server replica-b ${REPLICAS[1]}:5000 check inter 3s fall 2 rise 2
EOF
fi

cat > Caddyfile <<EOF
{
  email $ACME_EMAIL
}

$ADMIN_DOMAIN {
  reverse_proxy email:5000
}
EOF

if [[ -n "$QUERY_DOMAIN" ]]; then
  cat >> Caddyfile <<EOF

$QUERY_DOMAIN {
  @allowed path /show/* /query/* /api/v2/mailboxes/* /api/v1/mailboxes/messages /static/* /health/ready
  handle @allowed {
    reverse_proxy query-gateway:8000
  }
  respond 403
}
EOF
fi

docker pull "$IMAGE"
docker pull "$CADDY_IMAGE"
if [[ -n "$QUERY_DOMAIN" ]]; then docker pull haproxy:3.0-alpine; fi
compose up -d
echo "Primary deployed: https://$ADMIN_DOMAIN"
if [[ -n "$QUERY_DOMAIN" ]]; then
  echo "Query gateway deployed: https://$QUERY_DOMAIN"
  echo "After replicas are enrolled, set the public link base URL to https://$QUERY_DOMAIN in Verification Links settings."
fi
