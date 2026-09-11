#!/usr/bin/env bash
#
# ok-email 一键部署脚本
#
# 部署 lsh-okok/ok-email 的官方镜像，自动安装 Docker Engine 与 Docker Compose
# Plugin，生成 docker-compose.yml 与 .env，拉取镜像并启动服务。
#
# 用法:
#   bash <(curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/refs/heads/main/install.sh)
#   bash install.sh [--v VERSION] [--n NAME] [--p PORT] [--install-dir PATH]
#                           [--registry dockerhub|ghcr] [--image REPO:TAG]
#                           [--yes] [--show-credentials]
#
set -Eeuo pipefail

PROJECT_DIR="$(pwd -P)"
COMPOSE_FILE="$PROJECT_DIR/docker-compose.yml"
ENV_FILE="$PROJECT_DIR/.env"
CONTAINER_NAME="outlook-mail-reader"

DEFAULT_REGISTRY="dockerhub"
IMAGE_REPO="lsh-okok/ok-email"
GHCR_REPO="ghcr.io/lsh-okok/ok-email"
IMAGE_TAG="latest"
IMAGE_NAME="$IMAGE_REPO:$IMAGE_TAG"
REGISTRY_EXPLICIT=0
IMAGE_EXPLICIT=0

# 私有镜像仓库凭据。留空则按公开镜像匿名拉取。
REGISTRY_USERNAME="${OK_EMAIL_REGISTRY_USERNAME:-}"
REGISTRY_PASSWORD="${OK_EMAIL_REGISTRY_PASSWORD:-}"

readonly DEFAULT_PORT=5000
readonly MIN_PORT=1024
readonly MAX_PORT=65535
readonly CONTAINER_PORT=5000
readonly HEALTHCHECK_TIMEOUT_SECONDS="${HEALTHCHECK_TIMEOUT_SECONDS:-90}"
readonly HEALTHCHECK_INTERVAL_SECONDS="${HEALTHCHECK_INTERVAL_SECONDS:-3}"
readonly DEFAULT_DOCKER_COMPOSE_VERSION='v2.40.3'
DOCKER_COMPOSE_VERSION="${DOCKER_COMPOSE_VERSION:-$DEFAULT_DOCKER_COMPOSE_VERSION}"

ASSUME_YES=0
SHOW_CREDENTIALS=0
SUDO_KEEPALIVE_PID=''

log()  { printf '[ok-email] %s\n' "$*"; }
warn() { printf '[ok-email] WARNING: %s\n' "$*" >&2; }
die()  { printf '[ok-email] ERROR: %s\n' "$*" >&2; exit 1; }

# 能否向用户提问。注意 `bash <(curl ...)` / `curl | bash` 场景下 stdin 就是脚本
# 自身，直接 read 会把脚本后面的行当成用户输入读走，因此只读真实终端。
can_prompt() {
    [[ "$ASSUME_YES" == '0' ]] || return 1
    [[ -t 0 ]] && return 0
    [[ -c /dev/tty ]] && return 0
    return 1
}

# read_from_tty <变量名> <提示> [silent]
read_from_tty() {
    local __var="$1" __prompt="$2" __silent="${3:-0}" __val=''
    local -a __opts=(-r)
    [[ "$__silent" == '1' ]] && __opts+=(-s)

    if [[ -t 0 ]]; then
        read "${__opts[@]}" -p "$__prompt" __val || true
    elif [[ -c /dev/tty ]]; then
        read "${__opts[@]}" -p "$__prompt" __val < /dev/tty 2>/dev/null || return 1
    else
        return 1
    fi
    # 管道执行时行尾可能混入 CR，统一清洗
    __val="${__val//$'\r'/}"
    __val="${__val//$'\n'/}"
    printf -v "$__var" '%s' "$__val"
}

run_root() {
    if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
        "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo "$@"
    else
        die 'root privileges or sudo are required.'
    fi
}

docker_cmd() {
    if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
        docker "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo docker "$@"
    else
        die 'Docker access requires root privileges or sudo.'
    fi
}

compose_cmd() {
    docker_cmd compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

start_sudo_keepalive() {
    [[ "${EUID:-$(id -u)}" -eq 0 ]] && return 0
    command -v sudo >/dev/null 2>&1 || return 0
    ( while true; do sudo -v; sleep 60; done ) >/dev/null 2>&1 &
    SUDO_KEEPALIVE_PID=$!
}

stop_sudo_keepalive() {
    [[ -n "$SUDO_KEEPALIVE_PID" ]] || return 0
    kill "$SUDO_KEEPALIVE_PID" 2>/dev/null || true
    SUDO_KEEPALIVE_PID=''
}

validate_container_name() {
    local name="$1"
    [[ "$name" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ ]] \
        || die 'Container name must start with a letter or digit and use only letters, digits, dot, dash, or underscore.'
}

set_container_name() {
    validate_container_name "$1"
    CONTAINER_NAME="$1"
}

set_image_version() {
    local version="$1"
    [[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] \
        || die 'Image version must use letters, digits, dot, dash, or underscore.'
    IMAGE_TAG="$version"
}

select_registry() {
    case "$1" in
        dockerhub|docker|hub) IMAGE_REPO='lsh-okok/ok-email' ;;
        ghcr|github)          IMAGE_REPO="$GHCR_REPO" ;;
        *) die "--registry accepts dockerhub or ghcr." ;;
    esac
    REGISTRY_EXPLICIT=1
}

set_image_ref() {
    local ref="$1"
    # Reject characters that would break sed substitution or shell quoting.
    [[ "$ref" =~ ^[A-Za-z0-9][A-Za-z0-9._:@/-]*$ ]] \
        || die 'Image reference may only use letters, digits, dot, dash, underscore, slash, colon and @.'
    [[ "$ref" != *'&'* && "$ref" != *'|'* ]] \
        || die 'Image reference contains unsafe characters.'
    IMAGE_NAME="$ref"
    IMAGE_EXPLICIT=1
}

resolve_image_name() {
    [[ "$IMAGE_NAME" == "$IMAGE_REPO:"* ]] || return 0
    IMAGE_NAME="$IMAGE_REPO:$IMAGE_TAG"
}

# 从镜像引用里推断 registry 主机名：带域名（含点/冒号/localhost）的第一段就是
# registry，否则按 Docker Hub 处理。
registry_host_of() {
    local ref="$1" first
    case "$ref" in
        */*)
            first="${ref%%/*}"
            if [[ "$first" == *.* || "$first" == *:* || "$first" == 'localhost' ]]; then
                printf '%s' "$first"
            else
                printf 'docker.io'
            fi
            ;;
        *) printf 'docker.io' ;;
    esac
}

# 拉取失败时可切换的另一个仓库；用户已经明确指定过 --registry/--image 时不切换。
fallback_image_repo() {
    (( REGISTRY_EXPLICIT == 0 )) && (( IMAGE_EXPLICIT == 0 )) || return 0
    if [[ "$IMAGE_REPO" == "$GHCR_REPO" ]]; then
        printf '%s' 'lsh-okok/ok-email'
    else
        printf '%s' "$GHCR_REPO"
    fi
}

registry_login() {
    local host="$1" user="$REGISTRY_USERNAME" pass="$REGISTRY_PASSWORD"
    [[ -n "$user" ]] || return 0
    if [[ -z "$pass" ]]; then
        if can_prompt; then
            read_from_tty pass "Password (or PAT) for $user at $host: " 1 || true
            printf '\n' >&2
        fi
        pass="${pass//$'\r'/}"
        pass="${pass//$'\n'/}"
        [[ -n "$pass" ]] \
            || die "A registry username was supplied without a password; pass --registry-password or set OK_EMAIL_REGISTRY_PASSWORD."
    fi
    log "Logging in to $host as $user."
    printf '%s' "$pass" | docker_cmd login "$host" --username "$user" --password-stdin \
        || die "docker login to $host failed. Check the username and the token/password."
}

pull_and_start() {
    local host
    host="$(registry_host_of "$IMAGE_NAME")"
    registry_login "$host"
    log "Pulling $IMAGE_NAME."
    compose_cmd pull || return $?
    compose_cmd up -d
}

write_compose_file() {
    local backup
    mkdir -p "$PROJECT_DIR"
    if [[ -f "$COMPOSE_FILE" ]]; then
        backup="$COMPOSE_FILE.bak.$(date +%Y%m%d%H%M%S)"
        cp -a "$COMPOSE_FILE" "$backup"
        warn "Existing docker-compose.yml backed up to $(basename "$backup")."
    fi

    cat >"$COMPOSE_FILE" <<'COMPOSE'
services:
  outlook-mail-reader:
    image: __OK_EMAIL_IMAGE__
    container_name: __OK_EMAIL_CONTAINER__
    ports:
      - "${OUTLOOK_EMAIL_PORT:-5000}:5000"
    volumes:
      - ./data:/app/data
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - LOGIN_PASSWORD=${LOGIN_PASSWORD}
      - SECRET_KEY=${SECRET_KEY}
      - FLASK_ENV=production
      - DOCKER_UPDATE_ENABLED=true
      - DOCKER_UPDATE_CONTAINER=__OK_EMAIL_CONTAINER__
      # 可选：在较新的 Docker daemon / socket 代理环境中显式指定 API 版本
      # - DOCKER_UPDATE_API_VERSION=1.52
      # 可选：让界面 OAuth 助手使用你自己的 Azure 应用
      # - OAUTH_CLIENT_ID=your-azure-application-client-id
      # - OAUTH_REDIRECT_URI=http://localhost:8080
    restart: unless-stopped
COMPOSE
    sed -i "s|__OK_EMAIL_IMAGE__|$IMAGE_NAME|g" "$COMPOSE_FILE"
    sed -i "s|__OK_EMAIL_CONTAINER__|$CONTAINER_NAME|g" "$COMPOSE_FILE"
}

detect_os() {
    local os_release_file="${OS_RELEASE_FILE:-/etc/os-release}"
    [[ -r "$os_release_file" ]] || die "Cannot read $os_release_file."
    local distro_id
    distro_id="$(awk -F= '$1 == "ID" { gsub(/["\r]/, "", $2); print $2; exit }' "$os_release_file")"
    case "${distro_id,,}" in
        ubuntu|debian|centos|rhel|rocky|almalinux|amzn) printf '%s\n' "${distro_id,,}" ;;
        *) die "Unsupported Linux distribution: ${distro_id:-unknown}. Supported: Ubuntu, Debian, CentOS, RHEL, Rocky Linux, AlmaLinux, Amazon Linux." ;;
    esac
}

require_linux_systemd() {
    [[ "$(uname -s)" == 'Linux' ]] || die 'This installer supports Linux servers only.'
    command -v systemctl >/dev/null 2>&1 || die 'systemctl is required; non-systemd environments are not supported.'
}

require_privileges() {
    if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
        return 0
    fi
    command -v sudo >/dev/null 2>&1 || die 'Run as root or install sudo before running this installer.'
    sudo -v || die 'Unable to obtain sudo privileges.'
    start_sudo_keepalive
}

apt_install_docker() {
    local distro="$1" codename architecture
    codename="$(awk -F= '$1 == "VERSION_CODENAME" { gsub(/["\r]/, "", $2); print $2; exit }' "${OS_RELEASE_FILE:-/etc/os-release}")"
    [[ -n "$codename" ]] || die 'Could not determine the Debian/Ubuntu release codename.'
    architecture="$(dpkg --print-architecture)"

    run_root apt-get update
    run_root apt-get install -y ca-certificates curl openssl gnupg
    run_root install -m 0755 -d /etc/apt/keyrings
    curl -fsSL "https://download.docker.com/linux/$distro/gpg" \
        | gpg --dearmor \
        | run_root tee /etc/apt/keyrings/docker.gpg >/dev/null
    run_root chmod a+r /etc/apt/keyrings/docker.gpg
    printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/%s %s stable\n' \
        "$architecture" "$distro" "$codename" \
        | run_root tee /etc/apt/sources.list.d/docker.list >/dev/null
    run_root apt-get update
    run_root apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

rpm_install_docker() {
    local package_manager="$1"
    if [[ "$package_manager" == 'dnf' ]]; then
        run_root dnf install -y dnf-plugins-core ca-certificates curl openssl
        run_root dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        run_root dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    else
        run_root yum install -y yum-utils ca-certificates curl openssl
        run_root yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        run_root yum install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    fi
}

amazon_linux_package_manager() {
    if command -v dnf >/dev/null 2>&1; then printf '%s\n' dnf
    elif command -v yum >/dev/null 2>&1; then printf '%s\n' yum
    else die 'Neither dnf nor yum is available on this Amazon Linux system.'; fi
}

amazon_linux_install_docker() {
    local package_manager
    package_manager="$(amazon_linux_package_manager)"
    run_root "$package_manager" install -y ca-certificates curl openssl docker
}

docker_compose_arch() {
    case "$(uname -m)" in
        x86_64|amd64) printf '%s\n' x86_64 ;;
        aarch64|arm64) printf '%s\n' aarch64 ;;
        *) die "Unsupported architecture for Docker Compose: $(uname -m)." ;;
    esac
}

install_compose_plugin_binary() (
    local architecture binary_name binary_url checksums checksums_url
    local expected_checksum plugin_dir plugin_path release_url temporary actual_checksum
    [[ "$DOCKER_COMPOSE_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] \
        || die 'DOCKER_COMPOSE_VERSION must use the form vMAJOR.MINOR.PATCH.'
    architecture="$(docker_compose_arch)" || return $?
    plugin_dir='/usr/local/lib/docker/cli-plugins'
    plugin_path="$plugin_dir/docker-compose"
    binary_name="docker-compose-linux-${architecture}"
    release_url="https://github.com/docker/compose/releases/download/${DOCKER_COMPOSE_VERSION}"
    binary_url="$release_url/$binary_name"
    checksums_url="$release_url/checksums.txt"
    temporary="$(mktemp)"; checksums="$(mktemp)"
    trap 'rm -f -- "$temporary" "$checksums"' EXIT
    curl -fL --retry 3 --connect-timeout 10 --output "$temporary" "$binary_url"
    curl -fL --retry 3 --connect-timeout 10 --output "$checksums" "$checksums_url"
    expected_checksum="$(awk -v binary="$binary_name" '{ f = $2; sub(/^\*/, "", f); if (f == binary) { print $1; exit } }' "$checksums")"
    [[ "$expected_checksum" =~ ^[[:xdigit:]]{64}$ ]] || die "No valid checksum found for $binary_name."
    actual_checksum="$(sha256sum "$temporary" | awk '{print $1}')"
    [[ "${actual_checksum,,}" == "${expected_checksum,,}" ]] || die 'Docker Compose checksum verification failed.'
    run_root install -d -m 0755 "$plugin_dir"
    run_root install -m 0755 "$temporary" "$plugin_path"
)

install_docker() {
    local distro="$1"
    case "$distro" in
        ubuntu|debian) apt_install_docker "$distro" ;;
        centos|rhel|rocky|almalinux)
            if command -v dnf >/dev/null 2>&1; then rpm_install_docker dnf
            elif command -v yum >/dev/null 2>&1; then rpm_install_docker yum
            else die 'Neither dnf nor yum is available on this CentOS-family system.'; fi ;;
        amzn) amazon_linux_install_docker ;;
        *) die "Unsupported distribution: $distro" ;;
    esac
}

ensure_host_tools() {
    local distro="$1" missing=() package_manager
    command -v curl >/dev/null 2>&1 || missing+=(curl)
    command -v openssl >/dev/null 2>&1 || missing+=(openssl)
    ((${#missing[@]} == 0)) && return 0

    log "Installing required host tools: ${missing[*]}"
    case "$distro" in
        ubuntu|debian)
            run_root apt-get update
            run_root apt-get install -y ca-certificates "${missing[@]}" ;;
        centos|rhel|rocky|almalinux)
            if command -v dnf >/dev/null 2>&1; then run_root dnf install -y ca-certificates "${missing[@]}"
            elif command -v yum >/dev/null 2>&1; then run_root yum install -y ca-certificates "${missing[@]}"
            else die 'Neither dnf nor yum is available to install required host tools.'; fi ;;
        amzn)
            package_manager="$(amazon_linux_package_manager)"
            run_root "$package_manager" install -y ca-certificates "${missing[@]}" ;;
        *) die "Unsupported distribution: $distro" ;;
    esac
}

ensure_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        log 'Docker is not installed; installing Docker CE from the official repository.'
        install_docker "$1"
    fi
    if ! docker_cmd info >/dev/null 2>&1; then
        log 'Starting Docker service.'
        run_root systemctl enable --now docker.service
    fi
    docker_cmd info >/dev/null 2>&1 || die 'Docker daemon is not available after startup.'
}

ensure_compose() {
    local distro="$1" package_manager
    if ! docker_cmd compose version >/dev/null 2>&1; then
        log 'Docker Compose v2 plugin is not installed; installing it.'
        case "$distro" in
            ubuntu|debian)
                run_root apt-get update
                run_root apt-get install -y docker-compose-plugin ;;
            centos|rhel|rocky|almalinux)
                if command -v dnf >/dev/null 2>&1; then run_root dnf install -y docker-compose-plugin
                elif command -v yum >/dev/null 2>&1; then run_root yum install -y docker-compose-plugin
                else die 'Neither dnf nor yum is available to install Docker Compose Plugin.'; fi ;;
            amzn)
                package_manager="$(amazon_linux_package_manager)"
                if run_root "$package_manager" install -y docker-compose-plugin; then
                    if docker_cmd compose version >/dev/null 2>&1; then
                        return 0
                    fi
                    warn 'The native docker-compose-plugin package did not provide Docker Compose; using the official plugin binary.'
                else
                    warn 'docker-compose-plugin is unavailable from the Amazon Linux repository; using the official plugin binary.'
                fi
                install_compose_plugin_binary ;;
            *) die "Unsupported distribution: $distro" ;;
        esac
    fi
    docker_cmd compose version >/dev/null 2>&1 || die 'Docker Compose v2 plugin is not available after installation.'
    log "$(docker_cmd compose version)"
}

is_valid_port() {
    local port="$1"
    [[ "$port" =~ ^[0-9]+$ ]] || return 1
    (( 10#$port >= MIN_PORT && 10#$port <= MAX_PORT ))
}

port_is_used_by_own_container() {
    docker_cmd ps --filter "name=^${CONTAINER_NAME}$" --format '{{.Ports}}' 2>/dev/null \
        | grep -Eq "(^|[, ])(0\.0\.0\.0:)?${1}->${CONTAINER_PORT}/tcp"
}

# 0 = occupied, 1 = free, 2 = unknown
listener_on_port() {
    local port="$1"
    if command -v ss >/dev/null 2>&1; then
        # Column 4 is Local Address:Port; skip the header row.
        if ss -ltn 2>/dev/null | awk 'NR > 1 { print $4 }' | grep -Eq "[:.]${port}$"; then
            return 0
        fi
        return 1
    fi
    if command -v lsof >/dev/null 2>&1; then
        if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
            return 0
        fi
        return 1
    fi
    return 2
}

is_port_available() {
    local port="$1" state
    is_valid_port "$port" || return 1
    port_is_used_by_own_container "$port" && return 0
    set +e
    listener_on_port "$port"
    state=$?
    set -e
    case "$state" in
        0) return 1 ;;
        1) return 0 ;;
        *)
            # Neither ss nor lsof: fall back to inspecting published container ports.
            ! docker_cmd ps --format '{{.Ports}}' 2>/dev/null | grep -Eq "(^|[, ])(0\.0\.0\.0:)?${port}->"
            ;;
    esac
}

next_free_port() {
    local start="$1" candidate limit
    limit=$((start + 200))
    ((limit > MAX_PORT)) && limit=$MAX_PORT
    for ((candidate = start; candidate <= limit; candidate++)); do
        if is_port_available "$candidate"; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

choose_port() {
    local configured_port="$1" explicit="$2" candidate
    if is_port_available "$configured_port"; then
        printf '%s\n' "$configured_port"
        return 0
    fi

    if [[ "$explicit" == '1' ]]; then
        die "Host port $configured_port is already in use. Choose another port."
    fi

    warn "Host port $configured_port is already in use."
    if ! can_prompt; then
        candidate="$(next_free_port $((configured_port + 1)))" \
            || die "No free host port found between $((configured_port + 1)) and $MAX_PORT."
        warn "No usable terminal for input: using port $candidate."
        printf '%s\n' "$candidate"
        return 0
    fi

    while true; do
        if ! read_from_tty candidate "Enter an unused host port ($MIN_PORT-$MAX_PORT): "; then
            candidate="$(next_free_port $((configured_port + 1)))" \
                || die "No free host port found between $((configured_port + 1)) and $MAX_PORT."
            warn "Cannot read from terminal: using port $candidate."
            printf '%s\n' "$candidate"
            return 0
        fi
        if ! is_valid_port "$candidate"; then
            warn "Port must be an integer between $MIN_PORT and $MAX_PORT."
            continue
        fi
        if is_port_available "$candidate"; then
            printf '%s\n' "$candidate"
            return 0
        fi
        warn "Host port $candidate is also in use."
    done
}

read_env_value() {
    local file="$1" key="$2"
    [[ -f "$file" ]] || return 0
    awk -v key="$key" '
        BEGIN { assignment = "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=" }
        $0 ~ assignment {
            value = $0
            sub(assignment, "", value)
            sub(/\r$/, "", value)
            sub(/^[[:space:]]+/, "", value)
            sub(/[[:space:]]+$/, "", value)
            first = substr(value, 1, 1)
            last = substr(value, length(value), 1)
            if (length(value) >= 2 && ((first == "\"" && last == "\"") || (first == "\047" && last == "\047"))) {
                value = substr(value, 2, length(value) - 2)
            }
            print value
            exit
        }
    ' "$file"
}

upsert_env_var() {
    local file="$1" key="$2" value="$3" directory temporary
    directory="$(dirname -- "$file")"
    mkdir -p "$directory"
    temporary="$(mktemp "$directory/.env.tmp.XXXXXX")"
    if [[ -f "$file" ]]; then
        awk -v key="$key" -v replacement="$key=$value" '
            BEGIN { assignment = "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=" }
            $0 ~ assignment {
                if (!updated) print replacement
                updated = 1
                next
            }
            { print }
            END { if (!updated) print replacement }
        ' "$file" >"$temporary"
    else
        printf '%s\n' "$key=$value" >"$temporary"
    fi
    chmod 600 "$temporary"
    mv -f -- "$temporary" "$file"
    chmod 600 "$file"
}

generate_secret_key() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    elif [[ -r /dev/urandom ]] && command -v od >/dev/null 2>&1; then
        od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
    else
        die 'No secure random source is available for SECRET_KEY.'
    fi
}

generate_login_password() {
    local value=''
    while ((${#value} < 16)); do
        if command -v openssl >/dev/null 2>&1; then
            value+="$(openssl rand -base64 24 | tr -dc 'A-Za-z0-9')"
        elif [[ -r /dev/urandom ]] && command -v od >/dev/null 2>&1; then
            value+="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
        else
            die 'No secure random source is available for LOGIN_PASSWORD.'
        fi
    done
    printf '%.16s\n' "$value"
}

validate_env_value() {
    local key="$1" value="$2"
    [[ -n "$value" ]] || die "$key cannot be empty after generation."
    [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || die "$key cannot contain newlines."
    [[ "$value" =~ ^[A-Za-z0-9_@%+=:,./-]+$ ]] \
        || die "$key contains characters that are unsafe for the generated .env file (allowed: letters, digits and _ @ % + = : , . / -)."
}

# Reads a secret interactively when possible; auto-generates in non-interactive runs.
prompt_secret() {
    local label="$1" current="$2" generator="$3" value=''
    if [[ -n "$current" ]]; then
        log "Reusing existing $label from .env." >&2
        printf '%s' "$current"
        return 0
    fi
    if can_prompt; then
        read_from_tty value "$label (press Enter to generate): " 1 || true
        # 换行只用于终端排版，绝不能进 stdout，否则会被调用方的 $(...) 一起捕获，
        # 变成密码的一部分（表现为 "cannot contain newlines"）。
        printf '\n' >&2
    else
        warn "No usable terminal for input; generating $label automatically."
    fi
    value="${value//$'\r'/}"
    value="${value//$'\n'/}"
    # 读到的内容可能来自被占用的 stdin（例如 `curl | bash` 时读到脚本自身）或
    # 带有 Windows 行尾的粘贴内容。凡是不落在安全字符集内的输入一律丢弃，
    # 改用自动生成的随机值，避免把垃圾写进 .env。
    if [[ -n "$value" ]] && [[ ! "$value" =~ ^[A-Za-z0-9_@%+=:,./-]+$ ]]; then
        warn "Ignoring unusable input for $label; generating a secure value instead." >&2
        value=''
    fi
    [[ -n "$value" ]] || value="$("$generator")"
    value="${value//$'\r'/}"
    value="${value//$'\n'/}"
    printf '%s' "$value"
}

prepare_env() {
    local password secret
    password="$(prompt_secret LOGIN_PASSWORD "$(read_env_value "$ENV_FILE" LOGIN_PASSWORD || true)" generate_login_password)"
    secret="$(prompt_secret SECRET_KEY "$(read_env_value "$ENV_FILE" SECRET_KEY || true)" generate_secret_key)"
    validate_env_value LOGIN_PASSWORD "$password"
    validate_env_value SECRET_KEY "$secret"
    upsert_env_var "$ENV_FILE" LOGIN_PASSWORD "$password"
    upsert_env_var "$ENV_FILE" SECRET_KEY "$secret"
    chmod 600 "$ENV_FILE"
}

refresh_and_start() {
    if pull_and_start; then
        return 0
    fi
    # 默认 registry 拉取失败（例如镜像只发布在另一个 registry 上）时，
    # 若用户没有明确指定过来源，就自动换到另一个官方 registry 再试一次。
    local fallback_repo
    fallback_repo="$(fallback_image_repo)"
    [[ -n "$fallback_repo" ]] || return 1
    warn "Could not pull $IMAGE_NAME; retrying with $fallback_repo:$IMAGE_TAG."
    IMAGE_REPO="$fallback_repo"
    IMAGE_NAME="$fallback_repo:$IMAGE_TAG"
    write_compose_file
    if pull_and_start; then
        log "Falling back to $IMAGE_NAME."
        return 0
    fi
    return 1
}

explain_pull_failure() {
    cat >&2 <<HINT
[ok-email] ERROR: Docker image pull or Compose startup failed.

The image could not be pulled. Common causes:
  1. The image is private and this host is not logged in:
       bash install.sh --registry ghcr --registry-username YOUR_GITHUB_USER
     (you will be asked for a GitHub PAT with the read:packages scope)
  2. The tag does not exist yet. Check the published versions at
       https://github.com/lsh-okok/ok-email/pkgs/container/ok-email
  3. Network or proxy restrictions on the registry host.
HINT
}

show_failure_diagnostics() {
    warn 'Container health check failed. Recent diagnostics:'
    compose_cmd ps || true
    docker_cmd logs --tail 100 "$CONTAINER_NAME" || true
}

health_check_ok() {
    local port="$1"
    curl -fsS --max-time 5 "http://127.0.0.1:${port}/health/live" >/dev/null 2>&1 && return 0
    curl -fsS --max-time 5 "http://127.0.0.1:${port}/" >/dev/null 2>&1 && return 0
    return 1
}

wait_for_health() {
    local port="$1" deadline=$((SECONDS + HEALTHCHECK_TIMEOUT_SECONDS))
    while (( SECONDS < deadline )); do
        if health_check_ok "$port"; then
            return 0
        fi
        sleep "$HEALTHCHECK_INTERVAL_SECONDS"
    done
    show_failure_diagnostics
    return 1
}

print_summary() {
    local port="$1" password
    printf '\nok-email installation completed.\n'
    printf 'URL:        http://SERVER_IP:%s\n' "$port"
    printf 'Container:  %s\n' "$CONTAINER_NAME"
    printf 'Image:      %s\n' "$IMAGE_NAME"
    printf 'Config:     %s (mode 600)\n' "$ENV_FILE"
    if [[ "$SHOW_CREDENTIALS" == '1' ]]; then
        password="$(read_env_value "$ENV_FILE" LOGIN_PASSWORD)"
        printf 'LOGIN_PASSWORD: %s\n' "$password"
        printf 'SECRET_KEY:     %s\n' "$(read_env_value "$ENV_FILE" SECRET_KEY)"
        warn 'Credentials were printed to the terminal; clear your scrollback when done.'
    else
        printf '\nCredentials are stored in %s and are not printed here.\n' "$ENV_FILE"
        printf 'Read them with:  sudo cat %s\n' "$ENV_FILE"
        printf 'Or re-run with --show-credentials to print them once.\n'
    fi
}

usage() {
    cat <<'USAGE'
Usage: install.sh [options]

Install Docker when needed, write the embedded Compose configuration,
configure .env, pull the ok-email image and start the service.

Options:
  --v VERSION              Image tag to deploy (default: latest)
  --n CONTAINER_NAME       Container name (default: outlook-mail-reader)
  --p PORT                 Host port mapped to container port 5000 (default: 5000)
  --install-dir PATH       Deployment directory (default: current directory)
  --project-dir PATH       Alias of --install-dir
  --registry dockerhub|ghcr
                           Image registry (default: dockerhub -> lsh-okok/ok-email,
                           ghcr -> ghcr.io/lsh-okok/ok-email)
  --image REPO:TAG         Full image reference, overrides --registry/--v
  --registry-username USER Username for a private registry (Docker Hub or GHCR)
  --registry-password PASS Password or PAT for --registry-username. Omit it and
                           the script prompts, or set OK_EMAIL_REGISTRY_PASSWORD
  --yes, -y                Non-interactive: auto-generate credentials and pick a
                           free port instead of prompting
  --show-credentials       Print LOGIN_PASSWORD and SECRET_KEY when finished
  -h, --help               Show this help

The container mounts /var/run/docker.sock so the web UI can update itself.
That grants the container root-equivalent control of this host; skip this
script and deploy manually if you are not comfortable with that.
USAGE
}

main() {
    local project_dir_arg='' distro configured_port='' requested_port='' selected_port
    local port_arg_set=0

    while (($# > 0)); do
        case "$1" in
            --v) [[ $# -ge 2 ]] || die '--v requires a value.'; set_image_version "$2"; shift 2 ;;
            --n) [[ $# -ge 2 ]] || die '--n requires a value.'; set_container_name "$2"; shift 2 ;;
            --p) [[ $# -ge 2 ]] || die '--p requires a value.'; requested_port="$2"; port_arg_set=1; shift 2 ;;
            --registry) [[ $# -ge 2 ]] || die '--registry requires a value.'; select_registry "$2"; shift 2 ;;
            --image) [[ $# -ge 2 ]] || die '--image requires a value.'; set_image_ref "$2"; shift 2 ;;
            --registry-username) [[ $# -ge 2 ]] || die '--registry-username requires a value.'; REGISTRY_USERNAME="$2"; shift 2 ;;
            --registry-password) [[ $# -ge 2 ]] || die '--registry-password requires a value.'; REGISTRY_PASSWORD="$2"; shift 2 ;;
            --install-dir|--project-dir) [[ $# -ge 2 ]] || die "$1 requires a path."; project_dir_arg="$2"; shift 2 ;;
            --yes|-y) ASSUME_YES=1; shift ;;
            --show-credentials) SHOW_CREDENTIALS=1; shift ;;
            -h|--help) usage; return 0 ;;
            *) die "Unknown argument: $1" ;;
        esac
    done

    trap stop_sudo_keepalive EXIT

    if [[ -n "$project_dir_arg" ]]; then
        mkdir -p "$project_dir_arg"
        PROJECT_DIR="$(cd -- "$project_dir_arg" && pwd -P)"
        COMPOSE_FILE="$PROJECT_DIR/docker-compose.yml"
        ENV_FILE="$PROJECT_DIR/.env"
    fi

    resolve_image_name
    require_linux_systemd
    require_privileges
    write_compose_file

    distro="$(detect_os)"
    log "Detected supported distribution: $distro"
    ensure_docker "$distro"
    ensure_host_tools "$distro"
    ensure_compose "$distro"
    command -v curl >/dev/null 2>&1 || die 'curl is required for the health check.'

    mkdir -p "$PROJECT_DIR/data"
    touch "$ENV_FILE"
    chmod 600 "$ENV_FILE"

    configured_port="$(read_env_value "$ENV_FILE" OUTLOOK_EMAIL_PORT || true)"
    if ((port_arg_set)); then
        is_valid_port "$requested_port" \
            || die "Host port must be an integer between $MIN_PORT and $MAX_PORT."
        configured_port="$requested_port"
    fi
    [[ -n "$configured_port" ]] || configured_port="$DEFAULT_PORT"
    selected_port="$(choose_port "$configured_port" "$port_arg_set")"

    prepare_env
    upsert_env_var "$ENV_FILE" OUTLOOK_EMAIL_PORT "$selected_port"

    if ! refresh_and_start; then
        show_failure_diagnostics
        explain_pull_failure
        exit 1
    fi
    wait_for_health "$selected_port" || die 'The container started but did not become healthy.'
    print_summary "$selected_port"
}

if [[ "${INSTALLER_LIB_ONLY:-0}" != '1' ]]; then
    main "$@"
fi
