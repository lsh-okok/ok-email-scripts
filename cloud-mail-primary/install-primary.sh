#!/usr/bin/env bash
set -euo pipefail
umask 077

# A versioned, allowlisted server source archive; no runtime config or data.
primary_source_url='https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/cloud-mail-primary/releases/cloud-mail-server-20261114-5aa762eb9251.tar.gz'
primary_source_sha256='5aa762eb92514756ff2a862abb9080e046f1bbe85361c7d0598285fd2cd481c4'

fail() { printf '%s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'HELP'
用法：sudo bash install-primary.sh [选项]
  --dir PATH          安装目录，默认 /opt/cloud-mail-primary
  --name NAME         独立容器名，默认 cloud-mail-primary
  --host HOST         访问 IP 或域名；默认检测本机 IPv4
  --port PORT         网页端口，默认 3000
  --app-url URL       对外 HTTP(S) 地址，默认 http://HOST:PORT
  --mail-domain NAME  邮箱域，默认 cloudmail.test
  --admin-email EMAIL 管理员邮箱，默认 admin@邮箱域
  --image IMAGE       使用本机已有的应用镜像，跳过下载和构建
  --source-dir PATH   从本机服务器版源码目录构建
  --help              显示帮助
自动安装 Docker、生成独立密钥和管理员密码、启动主节点管理服务。
默认提供管理、邮箱来源、链接及节点管理；MX/Postfix 收发尚未启用。
同目录重复执行只启动原实例并保留配置和数据，不执行升级。
HELP
}

primary_original_args=("$@")
primary_dir='/opt/cloud-mail-primary'
primary_name='cloud-mail-primary'
primary_host=''
primary_port='3000'
primary_app_url=''
primary_mail_domain='cloudmail.test'
primary_admin_email=''
primary_image_ref=''
primary_source_dir=''
declare -A primary_explicit=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir|--name|--host|--port|--app-url|--mail-domain|--admin-email|--image|--source-dir)
      [[ $# -ge 2 && -n $2 && $2 != --* ]] || fail "$1 缺少参数值。"
      primary_option=${1#--}; primary_option=${primary_option//-/_}
      primary_explicit["$primary_option"]=true
      case "$1" in
        --dir) primary_dir=$2 ;;
        --name) primary_name=$2 ;;
        --host) primary_host=$2 ;;
        --port) primary_port=$2 ;;
        --app-url) primary_app_url=$2 ;;
        --mail-domain) primary_mail_domain=$2 ;;
        --admin-email) primary_admin_email=$2 ;;
        --image) primary_image_ref=$2 ;;
        --source-dir) primary_source_dir=$2 ;;
      esac
      shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) fail "未知参数：$1。请执行 --help 查看用法。" ;;
  esac
done
[[ -z $primary_image_ref || -z $primary_source_dir ]] || fail '--image 和 --source-dir 不能同时使用。'
[[ $primary_dir == /* && $primary_dir != / && $primary_dir != *$'\n'* && $primary_dir != *$'\r'* ]] || fail '安装目录必须是根目录之外的绝对路径。'
[[ $primary_dir != */../* && $primary_dir != */.. && $primary_dir != */./* && $primary_dir != */. ]] || fail '安装目录不能包含 . 或 .. 路径段。'
primary_dir=${primary_dir%/}
[[ $primary_name =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$ ]] || fail '容器名只能包含字母、数字、点、下划线和连字符。'
[[ $primary_port =~ ^[0-9]{1,5}$ ]] && (( 10#$primary_port >= 1 && 10#$primary_port <= 65535 )) || fail '端口必须为 1 到 65535 的整数。'
primary_port=$((10#$primary_port))
[[ $(uname -s) == Linux ]] || fail '主节点安装脚本适用于 Linux 服务器。'
if [[ $(id -u) != 0 ]]; then
  command -v sudo >/dev/null 2>&1 || fail '请使用 root，或 sudo bash install-primary.sh 执行。'
  exec sudo bash "$0" "${primary_original_args[@]}"
fi

install_tools() {
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl openssl coreutils diffutils tar iproute2 "$@"
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y ca-certificates curl openssl coreutils diffutils tar iproute "$@"
  else fail '缺少依赖，请安装 Docker、curl、openssl、coreutils、tar 和 iproute2 后重试。'; fi
}
primary_missing=false
for primary_tool in curl sha256sum openssl tar awk ss; do
  command -v "$primary_tool" >/dev/null 2>&1 || primary_missing=true
done
if [[ $primary_missing == true ]]; then install_tools; fi
if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' '正在安装 Docker…'
  if command -v apt-get >/dev/null 2>&1; then install_tools docker.io
  elif command -v dnf >/dev/null 2>&1; then
    primary_distribution=''
    if [[ -r /etc/os-release ]]; then
      primary_distribution=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
    fi
    case "$primary_distribution" in
      fedora) install_tools moby-engine docker-cli ;;
      rhel|centos|rocky|almalinux)
        install_tools dnf-plugins-core
        dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        dnf install -y docker-ce docker-ce-cli containerd.io ;;
      *) fail '请先为此发行版安装 Docker，再重新执行脚本。' ;;
    esac
  fi
  if command -v systemctl >/dev/null 2>&1; then systemctl enable --now docker; fi
fi
if ! docker info >/dev/null 2>&1; then
  if command -v systemctl >/dev/null 2>&1; then systemctl start docker; fi
  docker info >/dev/null 2>&1 || fail '无法连接 Docker 服务，请检查服务状态后重试。'
fi

[[ ! -L $primary_dir ]] || fail '安装目录不能是符号链接。'
primary_parent=$(dirname "$primary_dir")
mkdir -p "$primary_parent"
primary_parent=$(cd "$primary_parent" && pwd -P)
primary_dir="$primary_parent/$(basename "$primary_dir")"
primary_state="$primary_dir/.install-state"
primary_env="$primary_dir/primary.env"
primary_first_install=true
primary_temporary=''
cleanup() { if [[ -n $primary_temporary ]]; then rm -rf -- "$primary_temporary"; fi; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM

read_state() {
  local primary_key=$1 primary_count
  primary_count=$(awk -v key="$primary_key=" 'index($0,key)==1 {n++} END {print n+0}' "$primary_state")
  [[ $primary_count == 1 ]] || fail '已有安装状态不完整，请保留配置和数据并人工检查。'
  sed -n "s/^$primary_key=//p" "$primary_state"
}
restore_parameter() {
  local primary_key=$1 primary_variable=$2 primary_option=$3 primary_saved
  primary_saved=$(read_state "$primary_key")
  if [[ ${primary_explicit[$primary_option]:-false} == true && ${!primary_variable} != "$primary_saved" ]]; then
    fail "本次 --${primary_option//_/-} 与已有安装不同，已停止；原配置和数据未修改。"
  fi
  printf -v "$primary_variable" '%s' "$primary_saved"
}

if [[ -e $primary_state || -L $primary_state ]]; then
  [[ -f $primary_state && ! -L $primary_state && -f $primary_env && ! -L $primary_env ]] || fail '已有安装状态或配置异常，已停止。'
  [[ $(read_state FORMAT) == 1 ]] || fail '不支持该安装状态版本。'
  [[ $(read_state INSTALL_DIR) == "$primary_dir" ]] || fail '已有安装目录与状态记录不一致。'
  restore_parameter CONTAINER_NAME primary_name name
  restore_parameter HTTP_PORT primary_port port
  restore_parameter HOST_ADDRESS primary_host host
  restore_parameter APP_URL primary_app_url app_url
  restore_parameter MAIL_DOMAIN primary_mail_domain mail_domain
  restore_parameter ADMIN_EMAIL primary_admin_email admin_email
  if [[ ${primary_explicit[image]:-false} == true ]]; then
    [[ $(read_state SOURCE_MODE) == image && $(read_state SOURCE_VALUE) == "$primary_image_ref" ]] || fail '本次镜像与已有安装不同，已停止。'
  fi
  if [[ ${primary_explicit[source_dir]:-false} == true ]]; then
    primary_source_dir=$(cd "$primary_source_dir" && pwd -P) || fail '源码目录不存在。'
    [[ $(read_state SOURCE_MODE) == local && $(read_state SOURCE_VALUE) == "$primary_source_dir" ]] || fail '本次源码目录与已有安装不同，已停止。'
  fi
  primary_instance_id=$(read_state INSTANCE_ID)
  primary_image_id=$(read_state IMAGE_ID)
  [[ $primary_instance_id =~ ^[0-9a-f]{32}$ && $primary_image_id =~ ^sha256:[0-9a-f]{64}$ ]] || fail '已有安装身份或镜像记录无效。'
  primary_env_checksum=$(sha256sum "$primary_env"); primary_env_checksum=${primary_env_checksum%% *}
  [[ $primary_env_checksum == "$(read_state ENV_SHA256)" ]] || fail '已有配置已改变，已停止；请核对配置后通过 Docker 运维已有实例。'
  primary_first_install=false
else
  if [[ -d $primary_dir ]]; then
    [[ -z $(ls -A "$primary_dir") ]] || fail '安装目录已有文件，无法确认归属。请选择新的 --dir；原文件未修改。'
  elif [[ -e $primary_dir ]]; then fail '安装路径已被其他文件占用。'; fi
fi

valid_domain() {
  local primary_domain=$1 primary_label
  [[ ${#primary_domain} -le 253 && $primary_domain == *.* && $primary_domain != *. && $primary_domain != .* ]] || return 1
  local IFS=.
  for primary_label in $primary_domain; do
    [[ $primary_label =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || return 1
  done
}
valid_host() {
  local primary_address=$1 primary_part
  [[ -n $primary_address && ${#primary_address} -le 253 && $primary_address != .* && $primary_address != *. ]] || return 1
  if [[ $primary_address =~ ^[0-9.]+$ ]]; then
    [[ $primary_address =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
    local IFS=.
    for primary_part in $primary_address; do (( 10#$primary_part <= 255 )) || return 1; done
  else
    local IFS=.
    for primary_part in $primary_address; do
      [[ $primary_part =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$ ]] || return 1
    done
  fi
}
primary_mail_domain=${primary_mail_domain,,}
valid_domain "$primary_mail_domain" || fail '--mail-domain 必须为有效域名。'
if [[ -z $primary_admin_email ]]; then primary_admin_email="admin@$primary_mail_domain"; fi
primary_admin_email=${primary_admin_email,,}
[[ $primary_admin_email =~ ^[a-z0-9._%+-]+@[a-z0-9.-]+$ && ${primary_admin_email##*@} == "$primary_mail_domain" ]] || fail '--admin-email 必须属于 --mail-domain。'
if [[ -z $primary_host ]]; then
  primary_addresses=$(hostname -I 2>/dev/null || true)
  for primary_candidate in $primary_addresses; do
    if [[ $primary_candidate =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ && $primary_candidate != 127.* && $primary_candidate != 0.0.0.0 ]]; then
      primary_host=$primary_candidate; break
    fi
  done
fi
valid_host "$primary_host" || fail '无法确定访问地址，请通过 --host 提供服务器 IPv4 或域名。'
if [[ -z $primary_app_url ]]; then primary_app_url="http://$primary_host:$primary_port"; fi
primary_app_url=${primary_app_url%/}
[[ $primary_app_url =~ ^https?://[a-zA-Z0-9][a-zA-Z0-9.-]*(:[0-9]{1,5})?$ ]] || fail '--app-url 必须是无路径、账号、查询参数的 HTTP(S) 站点地址。'
primary_url_authority=${primary_app_url#*://}
valid_host "${primary_url_authority%%:*}" || fail '--app-url 必须包含有效的 IPv4 或域名。'
if [[ $primary_url_authority == *:* ]]; then
  primary_url_port=${primary_url_authority##*:}
  (( 10#$primary_url_port >= 1 && 10#$primary_url_port <= 65535 )) || fail '--app-url 端口必须为 1 到 65535 的整数。'
fi
primary_container=$primary_name
primary_volume="$primary_name-data"
primary_build_tag="cloud-mail-primary:$primary_name"

if [[ $primary_first_install == false ]]; then
  docker container inspect "$primary_container" >/dev/null 2>&1 || fail '已有安装的容器缺失，已停止；请保留原卷和配置后人工恢复。'
  primary_existing_instance=$(docker container inspect --format '{{index .Config.Labels "cloud-mail.installation-id"}}' "$primary_container")
  primary_existing_role=$(docker container inspect --format '{{index .Config.Labels "cloud-mail.role"}}' "$primary_container")
  primary_existing_port=$(docker container inspect --format '{{range (index .HostConfig.PortBindings "3000/tcp")}}{{println .HostPort}}{{end}}' "$primary_container")
  primary_existing_image=$(docker container inspect --format '{{.Image}}' "$primary_container")
  primary_existing_mount=$(docker container inspect --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Type}}:{{.Name}}{{end}}{{end}}' "$primary_container")
  primary_existing_volume_instance=$(docker volume inspect --format '{{index .Labels "cloud-mail.installation-id"}}' "$primary_volume") || fail '已有安装的数据卷缺失，已停止；请保留配置后人工恢复。'
  [[ $primary_existing_instance == "$primary_instance_id" && $primary_existing_role == primary ]] || fail '同名容器不是本次安装创建的主节点，已停止。'
  [[ $primary_existing_port == "$primary_port" && $primary_existing_image == "$primary_image_id" ]] || fail '已有容器端口或镜像与记录不一致，已停止。'
  [[ $primary_existing_mount == "volume:$primary_volume" && $primary_existing_volume_instance == "$primary_instance_id" ]] || fail '已有容器数据挂载或数据卷身份与记录不一致，已停止。'
  printf '%s\n' '正在启动已有主节点，保留原配置和数据…'
  docker start "$primary_container" >/dev/null
else
  if docker container inspect "$primary_container" >/dev/null 2>&1; then fail '同名容器已存在，请换一个 --name；不会覆盖已有服务。'; fi
  if docker volume inspect "$primary_volume" >/dev/null 2>&1; then fail '同名数据卷已存在，请换一个 --name；不会接管旧数据。'; fi
  [[ -z $(ss -H -ltn "sport = :$primary_port") ]] || fail "端口 $primary_port 已被占用，请用 --port 指定空闲端口。"
  if [[ -n $primary_image_ref ]]; then
    [[ $primary_image_ref != -* && $primary_image_ref != *$'\n'* && $primary_image_ref != *$'\r'* ]] || fail '应用镜像参数无效。'
    primary_image_id=$(docker image inspect --format '{{.Id}}' "$primary_image_ref") || fail '--image 指定的应用镜像不存在，请先导入镜像。'
    primary_source_mode=image; primary_source_value=$primary_image_ref
  else
    if docker image inspect "$primary_build_tag" >/dev/null 2>&1; then fail '安装专用镜像标签已存在，请换一个 --name；不会覆盖已有镜像。'; fi
    primary_temporary=$(mktemp -d "${TMPDIR:-/tmp}/cloud-mail-primary.XXXXXX")
    if [[ -n $primary_source_dir ]]; then
      [[ $primary_source_dir != *$'\n'* && $primary_source_dir != *$'\r'* ]] || fail '--source-dir 不能包含换行符。'
      primary_source_dir=$(cd "$primary_source_dir" && pwd -P) || fail '--source-dir 指定的目录不存在。'
      primary_build_context=$primary_source_dir
      primary_source_mode=local; primary_source_value=$primary_source_dir
    else
      [[ $primary_source_url == https://* && $primary_source_sha256 =~ ^[0-9a-f]{64}$ ]] || fail '此安装脚本尚未配置有效的服务器安装包。'
      printf '%s\n' '正在下载并校验主节点安装包…'
      curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 600 --output "$primary_temporary/source.tar.gz" "$primary_source_url" || fail '服务器安装包下载失败。'
      printf '%s  %s\n' "$primary_source_sha256" "$primary_temporary/source.tar.gz" | sha256sum -c - >/dev/null || fail '服务器安装包 SHA-256 校验失败。'
      tar -tzf "$primary_temporary/source.tar.gz" > "$primary_temporary/paths" || fail '服务器安装包格式无效。'
      while IFS= read -r primary_archive_path; do
        [[ $primary_archive_path != /* && $primary_archive_path != *\\* && ! $primary_archive_path =~ (^|/)\.\.(/|$) ]] || fail '安装包包含不安全路径，已停止。'
      done < "$primary_temporary/paths"
      mkdir "$primary_temporary/source"
      tar -xzf "$primary_temporary/source.tar.gz" --no-same-owner -C "$primary_temporary/source"
      primary_build_context="$primary_temporary/source"
      primary_source_mode=archive; primary_source_value=$primary_source_url
    fi
    [[ -f "$primary_build_context/Dockerfile" && -f "$primary_build_context/docker/app-entrypoint.sh" && -f "$primary_build_context/mail-worker/src/server/index.js" ]] || fail '源码不是可部署的 Cloud Mail 服务器版。'
    printf '%s\n' '正在构建主节点镜像，首次构建需要访问镜像源和 npm，可能需要数分钟…'
    docker build --build-arg APP_BASE_PATH= --tag "$primary_build_tag" "$primary_build_context"
    primary_image_id=$(docker image inspect --format '{{.Id}}' "$primary_build_tag")
  fi
  [[ $primary_image_id =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'Docker 返回的应用镜像 ID 无效。'
  primary_instance_id=$(openssl rand -hex 16)
  primary_admin_password=$(openssl rand -hex 24)
  primary_jwt_secret=$(openssl rand -hex 32)
  primary_data_key=$(openssl rand -hex 32)
  [[ $primary_instance_id =~ ^[0-9a-f]{32}$ && $primary_admin_password =~ ^[0-9a-f]{48}$ && $primary_jwt_secret =~ ^[0-9a-f]{64}$ && $primary_data_key =~ ^[0-9a-f]{64}$ ]] || fail '无法生成安装所需的安全随机密钥。'
  mkdir -p "$primary_dir"
  chmod 700 "$primary_dir"
  cat > "$primary_env" <<ENV
NODE_ENV=production
NODE_ROLE=primary
TZ=UTC
MAIL_DOMAINS=$primary_mail_domain
MAIL_HOST=mx.$primary_mail_domain
ADMIN_EMAIL=$primary_admin_email
ADMIN_PASSWORD=$primary_admin_password
JWT_SECRET=$primary_jwt_secret
DATA_ENCRYPTION_KEY=$primary_data_key
DATA_ENCRYPTION_KEY_ID=v1
APP_URL=$primary_app_url
APP_BASE_PATH=
PORT=3000
DATA_DIR=/data
SQLITE_PATH=/data/cloud-mail.sqlite
OBJECTS_DIR=/data/objects
STATIC_DIR=/app/mail-worker/dist
LMTP_PORT=2525
POLICY_PORT=10040
SMTP_HOST=mail
SMTP_PORT=587
SMTP_REQUIRE_TLS=false
TRUSTED_PROXIES=
ENV
  chmod 600 "$primary_env"
  unset primary_jwt_secret primary_data_key
  docker volume create --label "cloud-mail.installation-id=$primary_instance_id" "$primary_volume" >/dev/null
  printf '%s\n' '正在启动主节点管理服务…'
  docker run -d --name "$primary_container" --label "cloud-mail.installation-id=$primary_instance_id" --label cloud-mail.role=primary \
    --restart unless-stopped --security-opt no-new-privileges:true --env-file "$primary_env" \
    -p "$primary_port:3000" -v "$primary_volume:/data" "$primary_image_id" >/dev/null || fail "启动失败，配置和数据卷已保留在 $primary_dir；请人工检查，脚本不会覆盖部分安装。"
  primary_env_checksum=$(sha256sum "$primary_env"); primary_env_checksum=${primary_env_checksum%% *}
  cat > "$primary_dir/.install-state.tmp" <<STATE
FORMAT=1
INSTALL_DIR=$primary_dir
CONTAINER_NAME=$primary_container
HTTP_PORT=$primary_port
HOST_ADDRESS=$primary_host
APP_URL=$primary_app_url
MAIL_DOMAIN=$primary_mail_domain
ADMIN_EMAIL=$primary_admin_email
SOURCE_MODE=$primary_source_mode
SOURCE_VALUE=$primary_source_value
INSTANCE_ID=$primary_instance_id
IMAGE_ID=$primary_image_id
ENV_SHA256=$primary_env_checksum
STATE
  chmod 600 "$primary_dir/.install-state.tmp"
  mv "$primary_dir/.install-state.tmp" "$primary_state"
fi

printf '%s\n' '正在等待主节点就绪（最多 180 秒）…'
primary_deadline=$(( $(date +%s) + 180 ))
while (( $(date +%s) < primary_deadline )); do
  primary_remaining=$(( primary_deadline - $(date +%s) ))
  (( primary_remaining > 0 )) || break
  primary_timeout=$(( primary_remaining * 1000 ))
  if (( primary_timeout > 4000 )); then primary_timeout=4000; fi
  if docker exec "$primary_container" node -e 'fetch("http://127.0.0.1:3000/health/ready",{signal:AbortSignal.timeout(Number(process.argv[1]))}).then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))' "$primary_timeout" >/dev/null 2>&1; then
    printf '\n主节点安装成功\n访问地址：%s\n管理员邮箱：%s\n' "$primary_app_url" "$primary_admin_email"
    if [[ $primary_first_install == true ]]; then printf '管理员初始密码：%s\n' "$primary_admin_password"; fi
    printf '配置文件：%s\n容器名称：%s\n数据卷：%s\n' "$primary_env" "$primary_container" "$primary_volume"
    printf '%s\n' '已提供主节点管理服务，可在「节点管理」新增并部署只读节点。' 'MX/Postfix 收发尚未启用；公网域名、HTTPS 和邮件服务需按服务器部署文档配置。'
    exit 0
  fi
  [[ $(docker container inspect --format '{{.State.Running}}' "$primary_container") == true ]] || fail "主节点容器已停止，请执行 docker logs $primary_container 排查；配置和数据已保留。"
  primary_remaining=$(( primary_deadline - $(date +%s) ))
  (( primary_remaining > 0 )) || break
  if (( primary_remaining > 5 )); then sleep 5; else sleep "$primary_remaining"; fi
done
fail "主节点尚未就绪，容器、配置和数据已保留。请执行 docker logs $primary_container 排查。"
