
#!/usr/bin/env bash
set -Eeuo pipefail

# Nginx Proxy Manager 一键安装脚本
COMPOSE_URL="https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/docker-compose.yml"
INSTALL_DIR="/opt/npm"

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; exit 1; }

# 检查运行权限
if [ "$(id -u)" -ne 0 ]; then
    error "请使用 root 用户运行此脚本"
fi

# 检查系统
if ! command -v curl >/dev/null 2>&1; then
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update
        apt-get install -y curl ca-certificates
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y curl ca-certificates
    elif command -v yum >/dev/null 2>&1; then
        yum install -y curl ca-certificates
    else
        error "请先安装 curl"
    fi
fi

# 安装 Docker
if ! command -v docker >/dev/null 2>&1; then
    info "正在安装 Docker..."
    curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
    sh /tmp/get-docker.sh
    rm -f /tmp/get-docker.sh
fi

# 启动 Docker
if command -v systemctl >/dev/null 2>&1; then
    systemctl enable --now docker
fi

docker info >/dev/null 2>&1 || error "Docker 服务不可用"

# 检查 Docker Compose
docker compose version >/dev/null 2>&1 || \
    error "未检测到 Docker Compose V2，请先安装 Compose 插件"

# 检查端口占用（已有 NPM 时允许重新部署）
mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

if [ ! -f docker-compose.yml ]; then
    for port in 80 81 443; do
        if command -v ss >/dev/null 2>&1 && \
           ss -ltnH | awk '{print $4}' | grep -Eq "(^|:)$port$"; then
            error "端口 $port 已被占用，请先处理端口冲突"
        fi
    done
fi

# 下载 GitHub 配置
info "正在下载 docker-compose.yml..."
curl -fL --retry 3 --connect-timeout 15 \
    "$COMPOSE_URL" -o docker-compose.yml.tmp || \
    error "配置文件下载失败"

docker compose -f docker-compose.yml.tmp config -q || \
    error "Docker Compose 配置校验失败"

# 已有配置则备份
if [ -f docker-compose.yml ]; then
    cp docker-compose.yml docker-compose.yml.bak
fi

mv docker-compose.yml.tmp docker-compose.yml

# 创建持久化目录
mkdir -p data letsencrypt

# 拉取镜像并启动
info "正在拉取 NPM 镜像..."
docker compose pull

info "正在启动 Nginx Proxy Manager..."
docker compose up -d

# 显示结果
IP=$(curl -4fsS --max-time 5 https://api.ipify.org || true)

echo ""
echo "=========================================="
echo " Nginx Proxy Manager 部署命令执行完成"
echo "=========================================="
echo " 安装目录：$INSTALL_DIR"
echo " 管理端口：81"
echo " HTTP端口：80"
echo " HTTPS端口：443"
echo ""
if [ -n "$IP" ]; then
    echo " 管理地址：http://${IP}:81"
else
    echo " 管理地址：http://服务器IP:81"
fi
echo ""
echo " 查看状态：cd $INSTALL_DIR && docker compose ps"
echo " 查看日志：cd $INSTALL_DIR && docker compose logs -f"
echo " 更新服务：cd $INSTALL_DIR && docker compose pull && docker compose up -d"
echo ""
echo " 请确保防火墙和云服务器安全组放行相关端口"
echo "=========================================="
