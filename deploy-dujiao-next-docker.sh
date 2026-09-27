#!/usr/bin/env bash
# Dujiao-Next single-host Docker deployment (Ubuntu/Linux).
# Run: sudo bash scripts/deploy-docker.sh [--build]
set -Eeuo pipefail

APP_NAME=dujiao-next
REDIS_NAME=dujiao-next-redis
NETWORK_NAME=dujiao-next-net
DATA_DIR=${DJ_DATA_DIR:-/opt/dujiao-next}
APP_PORT=${DJ_PORT:-8080}
BIND_IP=${DJ_BIND_IP:-0.0.0.0}
IMAGE=${DJ_IMAGE:-dujiaonext/dujiao-next:latest}
BUILD_LOCAL=false

usage() {
  cat <<'USAGE'
Usage: sudo bash scripts/deploy-docker.sh [--build]

By default the script pulls the official full-stack Docker image. --build builds
the current checkout, including local code changes, into dujiao-next:local.

Optional environment variables:
  DJ_DATA_DIR  Persistent data directory (default /opt/dujiao-next)
  DJ_PORT      Host HTTP port (default 8080)
  DJ_BIND_IP   Address to bind (default 0.0.0.0; use 127.0.0.1 behind a proxy)
  DJ_IMAGE     Image to pull when not using --build

Existing containers and configuration are reused. The script does not upgrade
or replace an existing installation.
USAGE
}

case ${1:-} in
  '') ;;
  --build) BUILD_LOCAL=true ;;
  --help|-h) usage; exit 0 ;;
  *) usage >&2; exit 2 ;;
esac
if (( $# > 1 )); then usage >&2; exit 2; fi

die() { printf 'Error: %s\n' "$*" >&2; exit 1; }
log() { printf '[dujiao-next] %s\n' "$*"; }
container_exists() { docker container inspect "$1" >/dev/null 2>&1; }
container_running() { [[ $(docker inspect -f '{{.State.Running}}' "$1") == true ]]; }
health_ok() { curl --fail --silent --max-time 3 "http://${HEALTH_HOST}:${APP_PORT}/health" >/dev/null; }

(( EUID == 0 )) || die 'Run this script with sudo.'
command -v docker >/dev/null || die 'Docker is not installed.'
command -v openssl >/dev/null || die 'openssl is required.'
command -v curl >/dev/null || die 'curl is required.'
docker info >/dev/null 2>&1 || die 'Docker daemon is not available.'
[[ $APP_PORT =~ ^[0-9]+$ ]] && (( APP_PORT >= 1 && APP_PORT <= 65535 )) || die 'DJ_PORT must be a valid TCP port.'
[[ $BIND_IP =~ ^[0-9A-Fa-f:.]+$ ]] || die 'DJ_BIND_IP must be a numeric IPv4 or IPv6 address.'
[[ $DATA_DIR == /* && $DATA_DIR != / ]] || die 'DJ_DATA_DIR must be an absolute directory other than /.'
case $BIND_IP in
  0.0.0.0) HEALTH_HOST=127.0.0.1 ;;
  ::) HEALTH_HOST='[::1]' ;;
  *:*) HEALTH_HOST="[$BIND_IP]" ;;
  *) HEALTH_HOST=$BIND_IP ;;
esac

if container_exists "$APP_NAME"; then
  container_exists "$REDIS_NAME" || die "Existing $APP_NAME container has no $REDIS_NAME container."
  container_running "$REDIS_NAME" || docker start "$REDIS_NAME" >/dev/null
  if ! container_running "$APP_NAME"; then
    log 'Starting existing application container.'
    docker start "$APP_NAME" >/dev/null
  else
    log 'Existing application container is already running.'
  fi
  for attempt in {1..30}; do
    if health_ok; then
      log "Ready: http://${HEALTH_HOST}:${APP_PORT}/ (admin: /admin/)"
      exit 0
    fi
    sleep 2
  done
  docker logs --tail 50 "$APP_NAME" >&2 || true
  die 'Existing application did not pass its health check.'
fi

if [[ $BUILD_LOCAL == true ]]; then
  SOURCE_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
  [[ -f $SOURCE_DIR/Dockerfile && -f $SOURCE_DIR/go.mod ]] || die '--build must run from a Dujiao-Next source checkout.'
  IMAGE=dujiao-next:local
  log "Building local source from $SOURCE_DIR (this may take several minutes)."
  docker build -t "$IMAGE" "$SOURCE_DIR"
else
  log "Pulling $IMAGE"
  docker pull "$IMAGE"
fi

if ! container_exists "$REDIS_NAME"; then
  docker pull redis:7-alpine
fi

if [[ ! -f $DATA_DIR/config.yml ]]; then
  [[ ! -e $DATA_DIR/config.yml ]] || die "$DATA_DIR/config.yml exists but is not a regular file."
  [[ ! -e $DATA_DIR/db/dujiao.db ]] || die 'Database exists without config.yml; restore its original config and encryption key before starting.'
  umask 077
  install -d -m 0700 "$DATA_DIR" "$DATA_DIR/db" "$DATA_DIR/uploads" "$DATA_DIR/logs" "$DATA_DIR/redis"
  APP_SECRET=$(openssl rand -hex 32)
  ADMIN_JWT_SECRET=$(openssl rand -hex 32)
  USER_JWT_SECRET=$(openssl rand -hex 32)
  INITIAL_ADMIN_PASSWORD="Aa1$(openssl rand -hex 15)"
  cat > "$DATA_DIR/config.yml" <<EOF
app:
  secret_key: "$APP_SECRET"
server:
  host: 0.0.0.0
  port: 8080
  mode: release
  trusted_proxies: []
database:
  driver: sqlite
  dsn: /app/db/dujiao.db
  pool:
    max_open_conns: 1
    max_idle_conns: 1
jwt:
  secret: "$ADMIN_JWT_SECRET"
  expire_hours: 24
user_jwt:
  secret: "$USER_JWT_SECRET"
  expire_hours: 24
  remember_me_expire_hours: 168
bootstrap:
  default_admin_username: admin
  default_admin_password: "$INITIAL_ADMIN_PASSWORD"
redis:
  enabled: true
  host: $REDIS_NAME
  port: 6379
  db: 0
queue:
  enabled: true
  host: $REDIS_NAME
  port: 6379
  db: 1
email:
  enabled: false
cors:
  allowed_origins: ["http://127.0.0.1:$APP_PORT"]
  allow_credentials: true
web:
  admin_path: /admin
EOF
  printf '%s\n' "$INITIAL_ADMIN_PASSWORD" > "$DATA_DIR/initial-admin-password.txt"
  chmod 0600 "$DATA_DIR/config.yml" "$DATA_DIR/initial-admin-password.txt"
  log 'Generated new config and initial administrator password.'
else
  log "Reusing existing $DATA_DIR/config.yml"
fi
install -d -m 0700 "$DATA_DIR/db" "$DATA_DIR/uploads" "$DATA_DIR/logs" "$DATA_DIR/redis"

docker network inspect "$NETWORK_NAME" >/dev/null 2>&1 || docker network create "$NETWORK_NAME" >/dev/null
if container_exists "$REDIS_NAME"; then
  container_running "$REDIS_NAME" || docker start "$REDIS_NAME" >/dev/null
else
  docker run -d --name "$REDIS_NAME" --restart unless-stopped \
    --network "$NETWORK_NAME" -v "$DATA_DIR/redis:/data" \
    redis:7-alpine redis-server --appendonly yes >/dev/null
fi

log "Starting application on $BIND_IP:$APP_PORT"
docker run -d --name "$APP_NAME" --restart unless-stopped \
  --network "$NETWORK_NAME" -p "$BIND_IP:$APP_PORT:8080" \
  -v "$DATA_DIR/config.yml:/app/config.yml:ro" \
  -v "$DATA_DIR/db:/app/db" \
  -v "$DATA_DIR/uploads:/app/uploads" \
  -v "$DATA_DIR/logs:/app/logs" \
  "$IMAGE" >/dev/null

for attempt in {1..30}; do
  if health_ok; then
    if [[ -n ${INITIAL_ADMIN_PASSWORD:-} ]]; then
      # Initialization has completed; do not retain the bootstrap secret in config.
      sed -i 's/^  default_admin_password:.*/  default_admin_password: ""/' "$DATA_DIR/config.yml"
      docker restart "$APP_NAME" >/dev/null
      for restart_attempt in {1..15}; do
        health_ok && break
        sleep 2
      done
      health_ok || die 'Application became unavailable after removing the bootstrap password.'
    fi
    log "Ready: http://${HEALTH_HOST}:${APP_PORT}/ (admin: /admin/)"
    if [[ -n ${INITIAL_ADMIN_PASSWORD:-} ]]; then
      printf 'Administrator: admin\nInitial password: %s\n' "$INITIAL_ADMIN_PASSWORD"
      log "The password is also in $DATA_DIR/initial-admin-password.txt (root only). Change it after first login."
    fi
    exit 0
  fi
  sleep 2
done
docker logs --tail 50 "$APP_NAME" >&2 || true
die 'Application did not pass its health check within 60 seconds.'
