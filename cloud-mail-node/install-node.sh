#!/bin/sh
set -eu
umask 077
fail() { printf '%s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'HELP'
用法：sudo sh install-node.sh --primary-url URL --install-token TOKEN [选项]
  --primary-url URL       目标节点可访问的主节点地址，可包含站点子路径
  --install-token TOKEN   主节点生成的短期部署令牌
  --port PORT             访问端口；省略时使用主节点部署设置
  --allow-insecure-http   仅受信局域网允许 HTTP
  --help                  显示此说明
自动下载节点专属配置和安装包，不需要手工准备 node.env 或镜像目录。
相同配置可以重复执行；部署令牌请妥善保管。
HELP
}
node_primary_url=''
node_install_token=''
node_port=''
node_allow_http=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --primary-url) [ "$#" -ge 2 ] || fail '--primary-url 缺少值。'; node_primary_url=$2; shift 2 ;;
    --install-token) [ "$#" -ge 2 ] || fail '--install-token 缺少值。'; node_install_token=$2; shift 2 ;;
    --port) [ "$#" -ge 2 ] || fail '--port 缺少值。'; node_port=$2; shift 2 ;;
    --allow-insecure-http) node_allow_http=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *) fail '未知参数，请执行 --help 查看用法。' ;;
  esac
done
[ -n "$node_primary_url" ] && [ -n "$node_install_token" ] || fail '必须提供 --primary-url 和 --install-token。'
case "$node_primary_url" in *[!a-zA-Z0-9:/._~%\[\]-]*) fail '主节点地址包含无效字符。' ;; esac
case "$node_primary_url" in
  https://*) ;;
  http://*) [ "$node_allow_http" = true ] || fail 'HTTP 地址必须显式添加 --allow-insecure-http。' ;;
  *) fail '主节点地址必须使用 HTTPS，或显式允许的 HTTP。' ;;
esac
while [ "${node_primary_url%/}" != "$node_primary_url" ]; do node_primary_url=${node_primary_url%/}; done
case "$node_primary_url" in https://?*|http://?*) ;; *) fail '主节点地址缺少服务器地址。' ;; esac
node_authority=${node_primary_url#*://}
node_authority=${node_authority%%/*}
[ -n "$node_authority" ] || fail '主节点地址缺少服务器地址。'
case "$node_install_token" in ''|*[!a-zA-Z0-9_-]*) fail '部署令牌格式无效。' ;; esac
[ "${#node_install_token}" -eq 43 ] || fail '部署令牌格式无效。'
validate_port() {
  case "$1" in ''|*[!0-9]*) fail '端口必须为1到65535的整数。' ;; esac
  [ "${#1}" -le 5 ] && [ "$1" -ge 1 ] && [ "$1" -le 65535 ] || fail '端口必须为1到65535的整数。'
}
if [ -n "$node_port" ]; then validate_port "$node_port"; fi
run_root() {
  if [ "$(id -u)" = 0 ]; then "$@";
  elif command -v sudo >/dev/null 2>&1; then sudo "$@";
  else fail '安装依赖需要 root 或 sudo 权限。'; fi
}
[ "$(uname -s)" = Linux ] || fail '此安装命令适用于 Linux 服务器。'
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    run_root apt-get update
    run_root env DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl
  elif command -v dnf >/dev/null 2>&1; then run_root dnf install -y ca-certificates curl
  else fail '请先安装 curl 或 wget 后重试。'; fi
fi
node_bootstrap=$(mktemp -d "${TMPDIR:-/tmp}/cloud-mail-node.XXXXXX")
trap 'rm -rf "$node_bootstrap"' EXIT HUP INT TERM
node_protocols='=https'
if [ "$node_allow_http" = true ]; then node_protocols='=http,https'; fi
download() {
  if command -v curl >/dev/null 2>&1; then
    curl --fail --silent --show-error --location --proto "$node_protocols" --proto-redir "$node_protocols" \
      --connect-timeout 15 --max-time 600 --output "$2" "$1"
  elif [ "$node_allow_http" = true ]; then wget -q --timeout=60 -O "$2" "$1"
  else wget -q --https-only --timeout=60 -O "$2" "$1"; fi
}
node_install_url="$node_primary_url/api/node-install/$node_install_token"
node_bundle_url="$node_install_url/bundle"
printf '%s\n' '正在获取节点接入配置…'
download "$node_install_url/config.env" "$node_bootstrap/config.download" || fail '接入配置下载失败，请检查主节点地址或重新生成部署命令。'
[ -s "$node_bootstrap/config.download" ] || fail '主节点返回了空的接入配置。'
sed 's/\r$//' "$node_bootstrap/config.download" > "$node_bootstrap/config.env"
# Downloaded dotenv values are data: never source or eval this file.
awk '/^[[:space:]]*(#.*)?$/ {next} /^[A-Z_][A-Z0-9_]*=/ {next} {exit 1}' "$node_bootstrap/config.env" || fail '接入配置格式无效。'
config_value() {
  node_key_count=$(awk -v key="$1=" 'index($0,key)==1 {n++} END {print n+0}' "$node_bootstrap/config.env")
  [ "$node_key_count" -eq 1 ] || fail '接入配置缺少必要字段或有重复字段。'
  sed -n "s/^$1=//p" "$node_bootstrap/config.env"
}
[ "$(config_value NODE_ROLE)" = replica ] || fail '接入配置不是只读节点配置。'
node_config_primary=$(config_value NODE_PRIMARY_URL)
while [ "${node_config_primary%/}" != "$node_config_primary" ]; do node_config_primary=${node_config_primary%/}; done
[ "$node_config_primary" = "$node_primary_url" ] || fail '接入配置的主节点地址与命令不一致。'
case "$node_primary_url" in http://*) [ "$(config_value NODE_ALLOW_INSECURE_HTTP)" = true ] || fail '接入配置没有允许 HTTP。' ;; esac
node_id=$(config_value NODE_ID)
case "$node_id" in ''|*[!a-zA-Z0-9_-]*) fail '接入配置的节点 ID 无效。' ;; esac
[ "${#node_id}" -le 128 ] || fail '节点 ID 过长。'
node_credential=$(config_value NODE_CREDENTIAL)
case "$node_credential" in ''|*[!a-zA-Z0-9_-]*) fail '接入配置的节点凭据无效。' ;; esac
[ "${#node_credential}" -eq 43 ] || fail '接入配置的节点凭据无效。'
unset node_credential
for node_secret_key in JWT_SECRET DATA_ENCRYPTION_KEY; do
  node_secret=$(config_value "$node_secret_key")
  [ "${#node_secret}" -ge 32 ] || fail '接入配置缺少有效的节点独立密钥。'
done
unset node_secret
node_config_port=$(config_value INSTALL_PORT)
validate_port "$node_config_port"
if [ -z "$node_port" ]; then node_port=$node_config_port; fi
node_port=$(printf '%s' "$node_port" | sed 's/^0*//')
node_bundle_sha256=$(config_value INSTALL_BUNDLE_SHA256)
[ "${#node_bundle_sha256}" -eq 64 ] || fail '安装包摘要无效。'
case "$node_bundle_sha256" in *[!0-9a-f]*) fail '安装包摘要无效。' ;; esac
node_bundle_image_id=$(config_value INSTALL_BUNDLE_IMAGE_ID)
if [ -n "$node_bundle_image_id" ]; then
  case "$node_bundle_image_id" in sha256:*) ;; *) fail '安装包镜像 ID 无效。' ;; esac
  node_digest=${node_bundle_image_id#sha256:}
  [ "${#node_digest}" -eq 64 ] || fail '安装包镜像 ID 无效。'
  case "$node_digest" in *[!0-9a-f]*) fail '安装包镜像 ID 无效。' ;; esac
  unset node_digest
fi
node_env_source="$node_bootstrap/node.env"
sed '/^INSTALL_[A-Z0-9_]*=/d' "$node_bootstrap/config.env" > "$node_env_source"
node_container="cloud-mail-node-$node_id"
node_volume="$node_container-data"
node_image="cloud-mail-node:$node_id"
node_install_root="${CLOUD_MAIL_NODE_INSTALL_DIR:-${HOME:?无法确定当前用户的主目录}/.cloud-mail/nodes/$node_id}"

case "$node_install_root" in /*) ;; *) fail '节点安装目录必须为绝对路径。' ;; esac

if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' '正在安装 Docker…'
  if command -v apt-get >/dev/null 2>&1; then
    run_root apt-get update
    run_root env DEBIAN_FRONTEND=noninteractive apt-get install -y docker.io ca-certificates coreutils diffutils
  elif command -v dnf >/dev/null 2>&1; then
    if [ -r /etc/os-release ]; then . /etc/os-release; fi
    case "${ID:-}" in
      fedora) run_root dnf install -y moby-engine docker-cli ca-certificates coreutils diffutils ;;
      rhel|centos|rocky|almalinux)
        run_root dnf install -y dnf-plugins-core ca-certificates coreutils diffutils
        run_root dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        run_root dnf install -y docker-ce docker-ce-cli containerd.io ;;
      *) fail '暂不支持此发行版自动安装 Docker，请安装 Docker 后重新执行。' ;;
    esac
  else fail '请先为此 Linux 发行版安装 Docker，再重新执行本命令。'; fi
  if command -v systemctl >/dev/null 2>&1; then run_root systemctl enable --now docker; fi
fi

node_missing_tools=false
node_tools='cmp sha256sum'
if [ -z "$node_bundle_image_id" ]; then node_tools="$node_tools tar"; fi
for node_tool in $node_tools; do
  command -v "$node_tool" >/dev/null 2>&1 || node_missing_tools=true
done
if [ "$node_missing_tools" = true ]; then
  printf '%s\n' '正在安装部署所需的校验工具…'
  if command -v apt-get >/dev/null 2>&1; then
    run_root apt-get update
    run_root env DEBIAN_FRONTEND=noninteractive apt-get install -y coreutils diffutils tar
  elif command -v dnf >/dev/null 2>&1; then
    run_root dnf install -y coreutils diffutils tar
  else fail '缺少部署工具，请安装 coreutils、diffutils 和 tar 后重试。'; fi
fi

node_docker_sudo=false
if ! docker info >/dev/null 2>&1; then
  if command -v systemctl >/dev/null 2>&1; then run_root systemctl start docker; fi
  if ! docker info >/dev/null 2>&1; then
    run_root docker info >/dev/null 2>&1 || fail '无法连接 Docker 服务，请检查服务状态与权限后重试。'
    node_docker_sudo=true
  fi
fi
docker_cmd() {
  if [ "$node_docker_sudo" = true ]; then run_root docker "$@"; else docker "$@"; fi
}

[ ! -L "$node_install_root" ] || fail '节点目录不能是符号链接。'
mkdir -p "$node_install_root"
chmod 700 "$node_install_root"
node_temp=$(mktemp -d "$node_install_root/.install-XXXXXX")
trap 'rm -rf "$node_temp" "$node_bootstrap"' EXIT HUP INT TERM
sed 's/\r$//' "$node_env_source" > "$node_temp/node.env"
[ ! -L "$node_install_root/node.env" ] || fail '节点配置不能是符号链接。'
if [ ! -e "$node_install_root/node.env" ]; then
  ln "$node_temp/node.env" "$node_install_root/node.env" 2>/dev/null || true
fi
cmp -s "$node_temp/node.env" "$node_install_root/node.env" || fail '已有节点配置与本次命令不一致，已停止；原配置和数据未修改。'
chmod 600 "$node_install_root/node.env"

node_existing=false
if docker_cmd container inspect "$node_container" >/dev/null 2>&1; then
  node_existing_id=$(docker_cmd container inspect --format '{{index .Config.Labels "cloud-mail.node-id"}}' "$node_container")
  [ "$node_existing_id" = "$node_id" ] || fail '同名容器已被其他服务占用，已停止。'
  node_existing_port=$(docker_cmd container inspect --format '{{range (index .HostConfig.PortBindings "3000/tcp")}}{{println .HostPort}}{{end}}' "$node_container")
  [ "$node_existing_port" = "$node_port" ] || fail "已有节点使用端口 $node_existing_port，本次指定 $node_port。已停止，请使用原端口重新执行。"
  if [ -n "$node_bundle_image_id" ]; then
    node_existing_image=$(docker_cmd container inspect --format '{{.Image}}' "$node_container")
    [ "$node_existing_image" = "$node_bundle_image_id" ] || fail '已有节点的镜像版本与安装包不同，已停止。'
  fi
  node_existing=true
fi

if [ "$node_existing" = true ]; then
  printf '%s\n' '正在启动已有节点，保留原配置与数据…'
  docker_cmd start "$node_container" >/dev/null
else
  node_tagged_image=$(docker_cmd image inspect --format '{{.Id}}' "$node_image" 2>/dev/null || true)
  if [ -n "$node_tagged_image" ]; then
    if [ -n "$node_bundle_image_id" ]; then
      [ "$node_tagged_image" = "$node_bundle_image_id" ] || fail '同名镜像与安装包版本不一致，已停止。'
    fi
  else
    printf '%s\n' '正在下载并校验节点安装包…'
    download "$node_bundle_url" "$node_temp/bundle.tar.gz" || fail '安装包下载失败，请重新生成部署命令后重试。'
    command -v sha256sum >/dev/null 2>&1 || fail '缺少 sha256sum，请安装 coreutils 后重试。'
    printf '%s  %s\n' "$node_bundle_sha256" "$node_temp/bundle.tar.gz" | sha256sum -c - >/dev/null || fail '安装包校验失败，已停止部署。'
    if [ -n "$node_bundle_image_id" ]; then
      printf '%s\n' '正在导入主节点提供的镜像，无需联网构建…'
      docker_cmd load --input "$node_temp/bundle.tar.gz" >/dev/null
      docker_cmd image inspect "$node_bundle_image_id" >/dev/null 2>&1 || fail '安装包未包含预期镜像，已停止部署。'
      docker_cmd tag "$node_bundle_image_id" "$node_image"
    else
      mkdir "$node_temp/source"
      tar -xzf "$node_temp/bundle.tar.gz" -C "$node_temp/source"
      printf '%s\n' '正在构建节点镜像，首次构建可能需要数分钟…'
      docker_cmd build --tag "$node_image" "$node_temp/source"
    fi
  fi
  printf '%s\n' '正在启动节点…'
  docker_cmd volume create "$node_volume" >/dev/null
  docker_cmd run -d --name "$node_container" --label "cloud-mail.node-id=$node_id" \
    --restart unless-stopped --security-opt no-new-privileges:true \
    --env-file "$node_install_root/node.env" \
    -e NODE_ROLE=replica -e PORT=3000 -e DATA_DIR=/data \
    -e SQLITE_PATH=/data/cloud-mail.sqlite -e OBJECTS_DIR=/data/objects \
    -e STATIC_DIR=/app/mail-worker/dist -e APP_BASE_PATH= \
    -e "APP_URL=http://localhost:$node_port" \
    -p "$node_port:3000" -v "$node_volume:/data" "$node_image" >/dev/null
fi

printf '%s\n' '服务已启动，正在等待节点完成首次同步（最多 180 秒）…'
node_deadline=$(( $(date +%s) + 180 ))
while :; do
  node_remaining=$(( node_deadline - $(date +%s) ))
  [ "$node_remaining" -gt 0 ] || break
  node_probe_timeout=$(( node_remaining * 1000 ))
  if [ "$node_probe_timeout" -gt 4000 ]; then node_probe_timeout=4000; fi
  if docker_cmd exec "$node_container" node -e 'fetch("http://127.0.0.1:3000/health/ready", {signal: AbortSignal.timeout(Number(process.argv[1]))}).then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))' "$node_probe_timeout" >/dev/null 2>&1; then
    printf '%s\n' "部署成功，节点已同步并可取信。访问端口：$node_port。"
    printf '%s\n' "配置已保存：$node_install_root/node.env"
    exit 0
  fi
  node_running=$(docker_cmd container inspect --format '{{.State.Running}}' "$node_container")
  [ "$node_running" = true ] || fail "节点容器已停止，请执行 docker logs $node_container 查看原因，再重新执行安装命令。"
  node_remaining=$(( node_deadline - $(date +%s) ))
  [ "$node_remaining" -gt 0 ] || break
  if [ "$node_remaining" -gt 5 ]; then sleep 5; else sleep "$node_remaining"; fi
done
fail "节点已启动但尚未就绪，服务和数据已保留。请在节点管理查看同步状态，或执行 docker logs $node_container 排查主节点连接。"
