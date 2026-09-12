#!/usr/bin/env bash
# =============================================================================
#  Dujiao-Next 一键部署脚本（Docker Compose）
#  依据官方文档: https://dujiao-next.com/deploy/docker-compose
#
#  支持两种方案:
#    1) SQLite + Redis        （轻量，单机 / 小流量推荐）
#    2) PostgreSQL + Redis    （生产环境推荐）
#
#  脚本会自动:
#    - 检查 docker / docker compose 依赖
#    - 生成三个彼此不同的强随机密钥（app.secret_key / jwt.secret / user_jwt.secret）
#    - 生成 Redis / PostgreSQL 随机密码
#    - 下载 config.yml 模板（失败时使用脚本内嵌模板兜底）
#    - 生成 .env 与 docker-compose.yml
#    - 可选生成外层 Nginx 反向代理配置
#    - 拉起服务并输出访问信息
#
#  用法:
#    chmod +x install-dujiao-next.sh && sudo ./install-dujiao-next.sh
#
#  提示: 请在目标 Linux 服务器上以 root 或 sudo 运行本脚本。
#        无需预装 Docker——若未安装会自动安装。海外服务器用 Docker 官方源（默认）；
#        中国大陆服务器建议设 DJ_DOCKER_MIRROR=Aliyun 走阿里云镜像加速。
# =============================================================================
set -euo pipefail

# ------------------------------ 颜色输出 -----------------------------------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; BLUE='\033[0;34m'; NC='\033[0m'
info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[ OK ]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()   { echo -e "${RED}[ERR ]${NC} $*"; exit 1; }

# ------------------------------ 随机串生成 ---------------------------------
gen_hex() {
  # 参数: 字节数。返回 2 倍长度的十六进制串（纯字母数字，无特殊字符，便于 sed / env 处理）
  local n="$1"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$n"
  else
    tr -dc 'a-f0-9' < /dev/urandom | head -c $((n * 2))
  fi
}

gen_admin_password() {
  # 生成符合默认密码策略的管理员密码（大写 + 小写 + 数字，长度 12）
  # 默认策略: min_length>=8, require_upper/lower/number=true, require_special=false
  local upper lower digit rest
  upper=$(tr -dc 'A-Z' < /dev/urandom | head -c 1)
  lower=$(tr -dc 'a-z' < /dev/urandom | head -c 1)
  digit=$(tr -dc '0-9' < /dev/urandom | head -c 1)
  rest=$(tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 9)
  echo "${upper}${lower}${digit}${rest}"
}

# ------------------------------ 依赖检查 -----------------------------------
DC=""

# 自动安装 Docker（含 compose 插件），使用阿里云镜像加速
install_docker() {
  local mirror_args=""
  if [ "${DJ_DOCKER_MIRROR:-}" = "Aliyun" ]; then
    mirror_args="--mirror Aliyun"
    warn "未检测到 Docker，开始自动安装（阿里云镜像加速，约需 1~3 分钟）..."
  else
    warn "未检测到 Docker，开始自动安装（Docker 官方源，约需 1~3 分钟）..."
  fi
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL https://get.docker.com | sh -s -- ${mirror_args} || err "Docker 安装失败，请手动安装后重试。"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- https://get.docker.com | sh -s -- ${mirror_args} || err "Docker 安装失败，请手动安装后重试。"
  else
    err "未找到 curl 或 wget，无法自动安装 Docker。"
  fi

  # 启动并设置开机自启
  if command -v systemctl >/dev/null 2>&1; then
    systemctl enable --now docker 2>/dev/null || true
  elif command -v service >/dev/null 2>&1; then
    service docker start 2>/dev/null || true
  fi

  # 将发起 sudo 的普通用户加入 docker 组（方便后续免密使用）
  if [ "$(id -u)" = "0" ] && [ -n "${SUDO_USER:-}" ]; then
    usermod -aG docker "$SUDO_USER" 2>/dev/null || true
    warn "已将用户 ${SUDO_USER} 加入 docker 组；重新登录（或执行 newgrp docker）后可直接使用 docker。"
  fi
  ok "Docker 安装完成"
}

# 兜底：手动安装 docker compose 插件（官方脚本通常会一并安装，一般不会走到这里）
install_compose_plugin() {
  local arch ver
  arch="$(uname -m)"
  case "$arch" in
    x86_64)  arch="x86_64" ;;
    aarch64) arch="aarch64" ;;
    arm64)   arch="aarch64" ;;
    *) err "不支持的架构: ${arch}，请手动安装 Docker Compose。" ;;
  esac
  ver="${DJ_COMPOSE_VERSION:-v2.29.1}"
  mkdir -p /usr/local/lib/docker/cli-plugins
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "https://github.com/docker/compose/releases/download/${ver}/docker-compose-linux-${arch}" \
      -o /usr/local/lib/docker/cli-plugins/docker-compose || err "Compose 下载失败。"
  else
    wget -q "https://github.com/docker/compose/releases/download/${ver}/docker-compose-linux-${arch}" \
      -O /usr/local/lib/docker/cli-plugins/docker-compose || err "Compose 下载失败。"
  fi
  chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
}

check_deps() {
  info "检查依赖环境 ..."

  # Docker 未安装则自动安装
  if ! command -v docker >/dev/null 2>&1; then
    install_docker
  fi

  # 确保 docker 守护进程可访问
  if ! docker info >/dev/null 2>&1; then
    warn "docker 守护进程未运行，尝试启动 ..."
    if command -v systemctl >/dev/null 2>&1; then
      systemctl enable --now docker 2>/dev/null || true
    elif command -v service >/dev/null 2>&1; then
      service docker start 2>/dev/null || true
    fi
    sleep 3
    docker info >/dev/null 2>&1 || err "docker 守护进程无法启动，请用 systemctl status docker 排查。"
  fi

  # 判断 compose 命令
  if docker compose version >/dev/null 2>&1; then
    DC="docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then
    DC="docker-compose"
  else
    warn "缺少 Docker Compose，尝试安装 Compose 插件 ..."
    install_compose_plugin
    if docker compose version >/dev/null 2>&1; then
      DC="docker compose"
    else
      err "Docker Compose 安装失败，请手动安装。"
    fi
  fi
  ok "docker 与 compose 可用（${DC}）"
}



# ------------------------------ 交互收集配置 -------------------------------
collect_config() {
  # 非交互模式：所有配置从环境变量读取（供远程自动化 / CI 使用）
  if [ "${DJ_NONINTERACTIVE:-0}" = "1" ]; then
    INSTALL_DIR="${DJ_INSTALL_DIR:-/opt/dujiao-next}"
    choice="${DJ_DB_DRIVER:-sqlite}"
    if [ "$choice" = "postgres" ]; then
      DB_DRIVER="postgres"
    else
      DB_DRIVER="sqlite"
    fi
    TAG="${DJ_TAG:-latest}"
    APP_PORT="${DJ_APP_PORT:-8080}"
    ADMIN_PATH="${DJ_ADMIN_PATH:-dj-mgmt-$(gen_hex 3)}"
    ADMIN_PATH="${ADMIN_PATH#/}"
    ADMIN_USER="${DJ_ADMIN_USER:-admin}"
    ADMIN_PASS="${DJ_ADMIN_PASS:-$(gen_admin_password)}"
    DOMAIN="${DJ_DOMAIN:-}"
    DJ_BIND="${DJ_BIND:-127.0.0.1}"   # 端口绑定地址: 127.0.0.1=仅本机; 0.0.0.0/空=所有接口
    return
  fi

  echo ""
  echo "=============================================================="
  echo "            Dujiao-Next 一键部署向导"
  echo "=============================================================="

  # 部署目录
  read -p "部署目录 (默认 /opt/dujiao-next): " INSTALL_DIR
  INSTALL_DIR="${INSTALL_DIR:-/opt/dujiao-next}"

  # 方案
  echo ""
  echo "请选择部署方案:"
  echo "  1) SQLite + Redis       （轻量，单机 / 小流量推荐）"
  echo "  2) PostgreSQL + Redis   （生产环境推荐）"
  read -p "请输入 1 或 2 (默认 1): " choice
  choice="${choice:-1}"
  if [ "$choice" = "2" ]; then
    DB_DRIVER="postgres"
    COMPOSE_FILE="postgres"
  else
    DB_DRIVER="sqlite"
    COMPOSE_FILE="sqlite"
  fi

  # 镜像版本
  read -p "镜像版本 TAG (默认 latest): " TAG
  TAG="${TAG:-latest}"

  # 应用端口
  read -p "应用对外端口 APP_PORT (默认 8080): " APP_PORT
  APP_PORT="${APP_PORT:-8080}"

  # 后台入口路径（随机生成，降低扫描风险）
  DEFAULT_ADMIN_PATH="dj-mgmt-$(gen_hex 3)"
  read -p "后台入口路径 (默认自动生成 ${DEFAULT_ADMIN_PATH}): " ADMIN_PATH
  ADMIN_PATH="${ADMIN_PATH:-${DEFAULT_ADMIN_PATH}}"
  ADMIN_PATH="${ADMIN_PATH#/}"   # 去掉用户可能多输入的斜杠

  # 管理员账号
  read -p "后台管理员用户名 (默认 admin): " ADMIN_USER
  ADMIN_USER="${ADMIN_USER:-admin}"

  # 管理员密码（默认随机强密码，需符合大写+小写+数字策略）
  DEFAULT_ADMIN_PASS="$(gen_admin_password)"
  read -p "后台管理员密码 (默认自动生成 ${DEFAULT_ADMIN_PASS}，需含大小写字母和数字): " ADMIN_PASS
  ADMIN_PASS="${ADMIN_PASS:-${DEFAULT_ADMIN_PASS}}"

  # Nginx 反代（可选）
  read -p "是否生成 Nginx 反向代理配置? [y/N]: " do_nginx
  do_nginx="${do_nginx:-n}"
  if [[ "$do_nginx" =~ ^[Yy]$ ]]; then
    read -p "站点域名 (例如 shop.example.com): " DOMAIN
    DOMAIN="${DOMAIN:-}"
  else
    DOMAIN=""
  fi

  echo ""
  info "配置汇总:"
  echo "    部署目录   : ${INSTALL_DIR}"
  echo "    数据库方案 : ${DB_DRIVER}"
  echo "    镜像版本   : ${TAG}"
  echo "    应用端口   : ${APP_PORT}"
  echo "    后台路径   : /${ADMIN_PATH}"
  echo "    管理员     : ${ADMIN_USER}"
  if [ -n "$DOMAIN" ]; then echo "    Nginx 域名 : ${DOMAIN}"; fi
  read -p "确认以上配置并开始部署? [Y/n]: " confirm
  confirm="${confirm:-y}"
  if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
    err "已取消部署。"
  fi
}

# ------------------------------ 生成随机凭据 -------------------------------
gen_secrets() {
  APP_SECRET="$(gen_hex 32)"       # app.secret_key
  JWT_SECRET="$(gen_hex 32)"       # jwt.secret
  USER_JWT_SECRET="$(gen_hex 32)"  # user_jwt.secret
  REDIS_PASSWORD="$(gen_hex 16)"   # Redis 密码
  POSTGRES_PASSWORD="$(gen_hex 16)"  # PostgreSQL 密码
  PG_DB="dujiao_next"
  PG_USER="dujiao"
}

# ------------------------------ 准备目录 -----------------------------------
prepare_dirs() {
  info "准备部署目录: ${INSTALL_DIR}"
  mkdir -p "${INSTALL_DIR}"/{config,data/db,data/uploads,data/logs,data/redis,data/postgres}
  # 关键: 避免日志/数据库目录权限不足（api 容器默认非 root 用户）
  chmod -R 0777 "${INSTALL_DIR}"/data/logs "${INSTALL_DIR}"/data/db \
    "${INSTALL_DIR}"/data/uploads "${INSTALL_DIR}"/data/redis "${INSTALL_DIR}"/data/postgres
  ok "目录已创建并设置权限"
}

# ------------------------------ 准备 config.yml ----------------------------
prepare_config() {
  local cfg="${INSTALL_DIR}/config/config.yml"

  # 已存在则询问是否覆盖
  if [ -f "$cfg" ]; then
    warn "已存在配置文件 ${cfg}"
    read -p "是否覆盖? [y/N]: " ov
    if [[ ! "$ov" =~ ^[Yy]$ ]]; then
      info "保留现有 config.yml，跳过模板下载与替换。"
      return
    fi
  fi

  # 优先从官方仓库下载模板，失败则使用内嵌模板
  if curl -fsSL --connect-timeout 10 -m 40 "https://raw.githubusercontent.com/dujiao-next/dujiao-next/main/config.yml.example" -o "$cfg" 2>/dev/null; then
    ok "已下载官方 config.yml 模板"
  else
    warn "模板下载失败，使用脚本内嵌模板。"
    cat > "$cfg" <<'CONFIG_EOF'
# Dujiao-Next 配置文件
#
# 首次启动前必须把下面三个占位密钥替换为彼此不同的强随机值，否则服务会拒绝启动。

app:
  secret_key: your-secret-key-change-in-production-please
  totp_issuer: Dujiao-Next

server:
  host: 0.0.0.0
  port: 8080
  mode: debug
  trusted_proxies:
    - 127.0.0.1/32
    - ::1/128

log:
  dir: ""
  filename: app.log
  max_size_mb: 100
  max_backups: 7
  max_age_days: 30
  compress: true

database:
  driver: sqlite
  dsn: ./db/dujiao.db
  pool:
    max_open_conns: 1
    max_idle_conns: 1
    conn_max_lifetime_seconds: 0
    conn_max_idle_time_seconds: 0

jwt:
  secret: your-secret-key-change-in-production-please
  expire_hours: 24

user_jwt:
  secret: user-secret-key-change-in-production-please
  expire_hours: 24
  remember_me_expire_hours: 168

bootstrap:
  default_admin_username: ""
  default_admin_password: ""

telegram_auth:
  enabled: false
  bot_username: ""
  bot_token: ""
  client_secret: ""
  oidc_redirect_uri: ""
  mini_app_url: ""
  login_expire_seconds: 300
  replay_ttl_seconds: 300

google_auth:
  enabled: false
  client_id: ""

redis:
  enabled: true
  host: 127.0.0.1
  port: 6379
  password: ""
  db: 0
  prefix: "dj"

queue:
  enabled: true
  host: 127.0.0.1
  port: 6379
  password: ""
  db: 1
  concurrency: 10
  queues:
    default: 10
    critical: 5
  upstream_sync_interval: "5m"

upload:
  max_size: 10485760
  allowed_types:
    - image/jpeg
    - image/png
    - image/gif
    - image/webp
    - image/svg+xml
  allowed_extensions:
    - .jpg
    - .jpeg
    - .png
    - .gif
    - .webp
    - .svg
  max_width: 4096
  max_height: 4096

cors:
  allowed_origins:
    - "*"
  allowed_methods:
    - GET
    - POST
    - PUT
    - PATCH
    - DELETE
    - OPTIONS
  allowed_headers:
    - Content-Type
    - Content-Length
    - Accept-Encoding
    - Authorization
    - Cache-Control
    - X-Requested-With
    - X-CSRF-Token
  allow_credentials: true
  max_age: 600

security:
  login_rate_limit:
    window_seconds: 300
    max_attempts: 5
    block_seconds: 900
  password_policy:
    min_length: 8
    require_upper: true
    require_lower: true
    require_number: true
    require_special: false

email:
  enabled: true
  host: smtp.xxx.com
  port: 465
  username: your-username
  password: your-password
  from: your-email
  from_name: your-name
  use_tls: false
  use_ssl: true
  verify_code:
    expire_minutes: 10
    send_interval_seconds: 60
    max_attempts: 5
    length: 6

order:
  payment_expire_minutes: 15
  max_refund_days: 30

reseller:
  enabled: false
  main_hosts:
    - localhost
    - 127.0.0.1
    - "::1"
  trusted_forwarded_host: false
  subdomain_base: ""
  self_apply_enabled: true
  settlement_confirm_days: 7

web:
  admin_path: "/admin"
CONFIG_EOF
  fi

  # ------------------------- 替换密钥与运行时配置 -------------------------
  sed -i \
    -e "s|secret_key: your-secret-key-change-in-production-please|secret_key: ${APP_SECRET}|" \
    -e "s|secret: your-secret-key-change-in-production-please|secret: ${JWT_SECRET}|" \
    -e "s|secret: user-secret-key-change-in-production-please|secret: ${USER_JWT_SECRET}|" \
    -e "s|^  password: \"\"|  password: \"${REDIS_PASSWORD}\"|g" \
    -e "s|host: 127.0.0.1|host: redis|g" \
    -e "s|admin_path: \"/admin\"|admin_path: \"/${ADMIN_PATH}\"|" \
    -e "s|mode: debug|mode: release|" \
    "$cfg"

  if [ "$DB_DRIVER" = "postgres" ]; then
    sed -i \
      -e "s|driver: sqlite|driver: postgres|" \
      -e "s|dsn: ./db/dujiao.db|dsn: host=postgres user=${PG_USER} password=${POSTGRES_PASSWORD} dbname=${PG_DB} port=5432 sslmode=disable TimeZone=Asia/Shanghai|" \
      "$cfg"
  else
    sed -i \
      -e "s|dsn: ./db/dujiao.db|dsn: /app/db/dujiao.db|" \
      "$cfg"
  fi

  ok "config.yml 已生成并写入随机密钥（${DB_DRIVER} 方案）"
}

# ------------------------------ 生成 .env ----------------------------------
prepare_env() {
  local envf="${INSTALL_DIR}/.env"
  cat > "$envf" <<ENV_EOF
TAG=${TAG}
TZ=Asia/Shanghai
APP_PORT=${APP_PORT}

# 默认管理员（仅首次初始化时生效，登录后台后请立即修改密码）
DJ_DEFAULT_ADMIN_USERNAME=${ADMIN_USER}
DJ_DEFAULT_ADMIN_PASSWORD=${ADMIN_PASS}

# Redis
REDIS_PASSWORD=${REDIS_PASSWORD}

# PostgreSQL（仅 PostgreSQL 方案使用）
POSTGRES_DB=${PG_DB}
POSTGRES_USER=${PG_USER}
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
ENV_EOF
  chmod 600 "$envf"
  ok ".env 已生成（权限 600）"
}

# ------------------------------ 生成 compose -------------------------------
prepare_compose() {
  local cf="${INSTALL_DIR}/docker-compose.yml"
  if [ "$DB_DRIVER" = "postgres" ]; then
    cat > "$cf" <<'COMPOSE_EOF'
services:
  redis:
    image: redis:7-alpine
    container_name: dujiaonext-redis
    restart: unless-stopped
    environment:
      REDIS_PASSWORD: ${REDIS_PASSWORD}
    command: ["redis-server", "--dir", "/data", "--appendonly", "yes", "--requirepass", "${REDIS_PASSWORD}"]
    volumes:
      - ./data/redis:/data
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "${REDIS_PASSWORD}", "ping"]
      interval: 10s
      timeout: 3s
      retries: 10
    networks:
      - dujiao-net

  postgres:
    image: postgres:16-alpine
    container_name: dujiaonext-postgres
    restart: unless-stopped
    environment:
      TZ: ${TZ}
      POSTGRES_DB: ${POSTGRES_DB}
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - ./data/postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 10s
      timeout: 5s
      retries: 10
    networks:
      - dujiao-net

  dujiao-next:
    image: dujiaonext/dujiao-next:${TAG}
    container_name: dujiao-next
    restart: unless-stopped
    environment:
      TZ: ${TZ}
      DJ_DEFAULT_ADMIN_USERNAME: ${DJ_DEFAULT_ADMIN_USERNAME}
      DJ_DEFAULT_ADMIN_PASSWORD: ${DJ_DEFAULT_ADMIN_PASSWORD}
    ports:
      - "127.0.0.1:${APP_PORT}:8080"
    volumes:
      - ./config/config.yml:/app/config.yml:ro
      - ./data/uploads:/app/uploads
      - ./data/logs:/app/logs
    depends_on:
      redis:
        condition: service_healthy
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/health"]
      interval: 10s
      timeout: 3s
      retries: 10
    networks:
      - dujiao-net

networks:
  dujiao-net:
    driver: bridge
COMPOSE_EOF
  else
    cat > "$cf" <<'COMPOSE_EOF'
services:
  redis:
    image: redis:7-alpine
    container_name: dujiaonext-redis
    restart: unless-stopped
    environment:
      REDIS_PASSWORD: ${REDIS_PASSWORD}
    command: ["redis-server", "--dir", "/data", "--appendonly", "yes", "--requirepass", "${REDIS_PASSWORD}"]
    volumes:
      - ./data/redis:/data
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "${REDIS_PASSWORD}", "ping"]
      interval: 10s
      timeout: 3s
      retries: 10
    networks:
      - dujiao-net

  dujiao-next:
    image: dujiaonext/dujiao-next:${TAG}
    container_name: dujiao-next
    restart: unless-stopped
    environment:
      TZ: ${TZ}
      DJ_DEFAULT_ADMIN_USERNAME: ${DJ_DEFAULT_ADMIN_USERNAME}
      DJ_DEFAULT_ADMIN_PASSWORD: ${DJ_DEFAULT_ADMIN_PASSWORD}
    ports:
      - "127.0.0.1:${APP_PORT}:8080"
    volumes:
      - ./config/config.yml:/app/config.yml:ro
      - ./data/db:/app/db
      - ./data/uploads:/app/uploads
      - ./data/logs:/app/logs
    depends_on:
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/health"]
      interval: 10s
      timeout: 3s
      retries: 10
    networks:
      - dujiao-net

networks:
  dujiao-net:
    driver: bridge
COMPOSE_EOF
  fi
  ok "docker-compose.yml 已生成（${DB_DRIVER} 方案）"

  # DJ_BIND != 127.0.0.1 时去掉 127.0.0.1 前缀，改为监听所有接口（便于直接通过公网/局域网 IP 访问）
  if [ "${DJ_BIND:-127.0.0.1}" != "127.0.0.1" ]; then
    sed -i "s|127.0.0.1:\${APP_PORT}:8080|\${APP_PORT}:8080|" "$cf"
    info "应用端口已改为监听所有接口（DJ_BIND=${DJ_BIND}）"
  fi
}

# ------------------------------ 生成 Nginx 配置 ----------------------------
prepare_nginx() {
  [ -n "$DOMAIN" ] || return 0
  local nf="${INSTALL_DIR}/config/dujiao-next.nginx.conf"
  cat > "$nf" <<NGINX_EOF
# Dujiao-Next 反向代理配置
# 用法示例: cp config/dujiao-next.nginx.conf /etc/nginx/conf.d/dujiao-next.conf
#          替换下方证书路径后: nginx -t && systemctl reload nginx
server {
    listen 443 ssl http2;
    server_name ${DOMAIN};
    ssl_certificate     /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;

    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:${APP_PORT};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
NGINX_EOF
  ok "Nginx 配置已生成: ${nf}"
}

# ------------------------------ 启动服务 -----------------------------------
start_services() {
  info "拉取镜像并启动服务 ..."
  cd "$INSTALL_DIR"
  $DC --env-file .env -f docker-compose.yml pull
  $DC --env-file .env -f docker-compose.yml up -d
  ok "服务已启动"
}

# ------------------------------ 输出摘要 -----------------------------------
print_summary() {
  echo ""
  echo "=============================================================="
  echo "                    部署完成！"
  echo "=============================================================="
  echo "  部署目录 : ${INSTALL_DIR}"
  echo "  数据库   : ${DB_DRIVER}"
  echo ""
  local BIND_DISPLAY="${DJ_BIND:-127.0.0.1}"
  echo "  端口绑定 : ${BIND_DISPLAY}  (127.0.0.1=仅本机; 0.0.0.0/空=所有接口)"
  echo "  前台访问 : http://${BIND_DISPLAY}:${APP_PORT}/"
  echo "  后台访问 : http://${BIND_DISPLAY}:${APP_PORT}/${ADMIN_PATH}/"
  echo ""
  echo "  管理员账号 : ${ADMIN_USER}"
  echo "  管理员密码 : ${ADMIN_PASS}"
  echo ""
  if [ -n "$DOMAIN" ]; then
    echo "  域名前台 : https://${DOMAIN}"
    echo "  域名后台 : https://${DOMAIN}/${ADMIN_PATH}/"
  fi
  echo "--------------------------------------------------------------"
  echo "  健康检查: curl http://127.0.0.1:${APP_PORT}/health"
  echo "  查看日志: cd ${INSTALL_DIR} && ${DC} --env-file .env -f docker-compose.yml logs -f dujiao-next"
  echo "--------------------------------------------------------------"
  warn "安全提示:"
  echo "  1. 登录后台后请立即修改管理员密码。"
  echo "  2. app.secret_key 必须与数据库一起备份，丢失将无法解密敏感数据。"
  echo "  3. Redis / PostgreSQL 未对外暴露端口，应用端口仅绑定 127.0.0.1，"
  echo "     请通过上方 Nginx 反代对外提供服务。"
  echo "=============================================================="
}

# ------------------------------ 主流程 -------------------------------------
main() {
  check_deps
  collect_config
  gen_secrets
  prepare_dirs
  prepare_config
  prepare_env
  prepare_compose
  prepare_nginx
  start_services
  print_summary
}

main "$@"
