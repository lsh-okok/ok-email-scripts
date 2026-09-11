#!/usr/bin/env bash
# ==============================================================================
# FileBrowser 一键部署脚本
# 适用: Ubuntu / Debian / CentOS / Rocky / Alma / Amazon Linux (systemd)
# 用法: sudo bash setup_filebrowser.sh [选项]
#   -p, --port     PORT      监听端口          (默认 8080)
#   -r, --root     DIR       文件管理根目录     (默认 /home/ubuntu)
#   -u, --user     NAME      管理员用户名       (默认 admin)
#   -P, --password PASS      管理员密码         (默认随机生成)
#       --update              仅升级二进制
#       --uninstall           卸载（默认保留数据库）
#       --purge               卸载并删除数据库与配置
#   -h, --help                查看帮助
# 环境变量(可选):
#   FB_VERSION=v2.63.23      指定版本
#   GH_PROXY=https://ghfast.top/   下载加速前缀(GitHub 不通时用)
# ==============================================================================
set -euo pipefail

C_RED='\033[31m'; C_GREEN='\033[32m'; C_YELLOW='\033[33m'; C_BLUE='\033[36m'; C_OFF='\033[0m'
log()  { echo -e "${C_BLUE}[INFO]${C_OFF} $*"; }
ok()   { echo -e "${C_GREEN}[ OK ]${C_OFF} $*"; }
warn() { echo -e "${C_YELLOW}[WARN]${C_OFF} $*"; }
err()  { echo -e "${C_RED}[FAIL]${C_OFF} $*" >&2; exit 1; }

# ---------- 默认参数 ----------
PORT="${PORT:-8080}"
ROOT_DIR="${ROOT_DIR:-/home/ubuntu}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-}"
ACTION="install"

INSTALL_DIR="/usr/local/bin"
BIN="${INSTALL_DIR}/filebrowser"
CONF_DIR="/etc/filebrowser"
DB_FILE="${CONF_DIR}/filebrowser.db"
LOG_FILE="/var/log/filebrowser.log"
SERVICE_FILE="/etc/systemd/system/filebrowser.service"
FALLBACK_VERSION="v2.63.23"

usage() { sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    -p|--port)     PORT="$2";       shift 2;;
    -r|--root)     ROOT_DIR="$2";   shift 2;;
    -u|--user)     ADMIN_USER="$2"; shift 2;;
    -P|--password) ADMIN_PASS="$2"; shift 2;;
    --update)      ACTION="update";    shift;;
    --uninstall)   ACTION="uninstall"; shift;;
    --purge)       ACTION="purge";     shift;;
    -h|--help)     usage;;
    *) err "未知参数: $1 (用 -h 查看帮助)";;
  esac
done

[[ $EUID -eq 0 ]] || err "请用 root 执行: sudo bash $0"

# ---------- 架构识别 ----------
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64";;
    aarch64|arm64)  echo "arm64";;
    armv7l|armv6l)  echo "armv7";;
    *) err "不支持的 CPU 架构: $(uname -m)";;
  esac
}

get_latest_version() {
  local v
  v=$(curl -fsSL --max-time 15 https://api.github.com/repos/filebrowser/filebrowser/releases/latest \
        | grep -m1 '"tag_name"' | cut -d'"' -f4 || true)
  [[ -n "$v" ]] && echo "$v" || echo "${FALLBACK_VERSION}"
}

# ---------- 卸载 ----------
do_uninstall() {
  local keep_db=1
  [[ "${ACTION}" == "purge" ]] && keep_db=0

  systemctl stop filebrowser 2>/dev/null || true
  systemctl disable filebrowser 2>/dev/null || true
  rm -f "${SERVICE_FILE}"
  rm -f "${BIN}"
  if [[ $keep_db -eq 0 ]]; then
    rm -rf "${CONF_DIR}" "${LOG_FILE}"
    ok "已卸载并清除配置与数据库"
  else
    ok "已卸载（数据库保留于 ${DB_FILE}）"
  fi
  systemctl daemon-reload
  exit 0
}
[[ "${ACTION}" == "uninstall" || "${ACTION}" == "purge" ]] && do_uninstall

# ---------- 依赖 ----------
install_deps() {
  local need=()
  for c in curl tar systemctl; do command -v "$c" >/dev/null || need+=("$c"); done
  if [[ ${#need[@]} -gt 0 ]]; then
    log "安装依赖: ${need[*]}"
    if command -v apt-get >/dev/null; then
      apt-get update -qq && apt-get install -y -qq curl tar systemd >/dev/null
    elif command -v dnf >/dev/null; then
      dnf install -y -q curl tar systemd >/dev/null
    elif command -v yum >/dev/null; then
      yum install -y -q curl tar systemd >/dev/null
    fi
  fi
}

# ---------- 下载并安装二进制 ----------
install_binary() {
  local arch ver url tmp
  arch=$(detect_arch)
  ver="${FB_VERSION:-$(get_latest_version)}"
  url="${GH_PROXY:-}https://github.com/filebrowser/filebrowser/releases/download/${ver}/linux-${arch}-filebrowser.tar.gz"

  log "版本 ${ver} / 架构 ${arch}"
  log "下载地址: ${url}"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN

  curl -fsSL --retry 3 --max-time 180 "${url}" -o "${tmp}/fb.tar.gz" \
    || err "下载失败。若在国内部署，可先设置加速前缀后重试:
       GH_PROXY=https://ghfast.top/ bash $0"

  tar -xzf "${tmp}/fb.tar.gz" -C "${tmp}" filebrowser \
    || err "解压失败，压缩包中未找到 filebrowser 二进制"

  install -m 0755 "${tmp}/filebrowser" "${BIN}"
  ok "已安装到 ${BIN} -> $(${BIN} version | head -1)"
}

# ---------- 初始化配置与管理员 ----------
init_config() {
  mkdir -p "${CONF_DIR}"
  [[ -d "${ROOT_DIR}" ]] || { warn "根目录 ${ROOT_DIR} 不存在，已创建"; mkdir -p "${ROOT_DIR}"; }

  if [[ ! -f "${DB_FILE}" ]]; then
    "${BIN}" -d "${DB_FILE}" config init >/dev/null
    "${BIN}" -d "${DB_FILE}" config set \
      --address 0.0.0.0 \
      --port "${PORT}" \
      --root "${ROOT_DIR}" \
      --baseurl / \
      --log "${LOG_FILE}" >/dev/null
  else
    warn "检测到已有数据库，仅更新端口/根目录，保留现有用户数据"
    "${BIN}" -d "${DB_FILE}" config set --address 0.0.0.0 --port "${PORT}" --root "${ROOT_DIR}" >/dev/null
  fi

  if ! "${BIN}" -d "${DB_FILE}" users ls 2>/dev/null | grep -qw "${ADMIN_USER}"; then
    [[ -z "${ADMIN_PASS}" ]] && ADMIN_PASS=$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 14)
    "${BIN}" -d "${DB_FILE}" users add "${ADMIN_USER}" "${ADMIN_PASS}" --perm.admin --lockPassword false >/dev/null
    ok "已创建管理员 ${ADMIN_USER} / ${ADMIN_PASS}"
  else
    if [[ -n "${ADMIN_PASS}" ]]; then
      "${BIN}" -d "${DB_FILE}" users update "${ADMIN_USER}" --password "${ADMIN_PASS}" >/dev/null
      ok "已更新 ${ADMIN_USER} 密码为 ${ADMIN_PASS}"
    else
      warn "管理员 ${ADMIN_USER} 已存在，密码保持不变（用 -P 重设）"
    fi
  fi
  chmod 600 "${DB_FILE}"
}

# ---------- systemd 服务 ----------
write_service() {
  cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=FileBrowser - Web File Manager
Documentation=https://filebrowser.org/
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=${ROOT_DIR}
ExecStart=${BIN} -d ${DB_FILE}
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable filebrowser >/dev/null 2>&1
  systemctl restart filebrowser
  sleep 2
}

open_firewall() {
  if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -qw active; then
    ufw allow "${PORT}/tcp" >/dev/null && ok "UFW 已放行 ${PORT}/tcp"
  elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="${PORT}/tcp" >/dev/null && firewall-cmd --reload >/dev/null \
      && ok "firewalld 已放行 ${PORT}/tcp"
  fi
}

# ---------- 主流程 ----------
install_deps
install_binary
[[ "${ACTION}" == "update" ]] && { write_service; ok "升级完成"; exit 0; }
init_config
write_service
open_firewall

IP=$(curl -fsSL --max-time 5 ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')

if systemctl is-active --quiet filebrowser; then
  ok "FileBrowser 运行中"
  if curl -fsS --max-time 5 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1 \
     || curl -fsS --max-time 5 "http://127.0.0.1:${PORT}/" -o /dev/null 2>&1; then
    ok "本地健康检查通过"
  fi
  echo
  echo -e "${C_GREEN}===== 部署完成 =====${C_OFF}"
  echo -e "  访问地址:  http://${IP}:${PORT}"
  echo -e "  管理员:    ${ADMIN_USER}"
  [[ -n "${ADMIN_PASS}" ]] && echo -e "  密码:      ${ADMIN_PASS}"
  echo -e "  根目录:    ${ROOT_DIR}"
  echo -e "  数据库:    ${DB_FILE}"
  echo
  echo -e "  常用命令:  systemctl status|restart|stop filebrowser"
  echo -e "             查看日志: journalctl -u filebrowser -f"
  echo
  warn "云服务器(EC2/阿里云等)还需在安全组放行 ${PORT} 端口"
  warn "建议配合 Nginx 反代 + HTTPS 暴露到公网"
else
  err "服务未启动成功，执行 journalctl -u filebrowser -n 50 查看原因"
fi
