#!/usr/bin/env bash
# Linux entry point; reuse the same deployment engine as the Windows uploader.
set -Eeuo pipefail
umask 077

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'HELP'
Mail Lite one-command installer (Ubuntu / Debian)

  sudo bash install-mail-lite.sh --fresh
  sudo bash install-mail-lite.sh
  sudo bash install-mail-lite.sh --yes
  sudo bash install-mail-lite.sh --mode direct
  sudo bash install-mail-lite.sh --yes --mode caddy --domain mail.example.com

Options:
  --fresh             Create a separate instance with an empty database
  --port PORT         Initial HTTP port (fresh: first free port from 8080)
  --dir PATH          Installation directory (default: /opt/mail-lite)
  --project NAME      Compose project name (default: mail-lite; reused on update)
  --source PATH       Local repository or mail-lite source directory
  --mode MODE         Explicitly configure direct, caddy (HTTPS), or npm
  --host HOST         Override automatically detected public IPv4/hostname
  --domain DOMAIN     Configure a website domain later (default mode: caddy)
  --npm-network NAME  Existing Nginx Proxy Manager Docker bridge network
  --mx-host HOST      Enable MX receiving with this public mail hostname
  --repo OWNER/REPO   GitHub source when no local source exists (lsh-okok/ok-email-scripts)
  --ref REF           GitHub branch/tag/commit (main)
  --yes               Use supplied options/defaults without prompting
  -h, --help          Show this help

The installer detects the public IPv4 and prints its HTTP management URL.
No domain is required. Local source takes priority over GitHub. Updates keep
server settings unless --mode, --domain or --mx-host is explicitly supplied.
Configuration changes are backed up; administrator credentials/data are kept.
With --fresh, a unique directory/project/volume and new credentials are created.
Existing installations are never reused; update a fresh instance using --dir.
HELP
}

original_args=("$@")
install_dir=/opt/mail-lite
project=mail-lite
source_arg=
mode=direct
public_host=
domain=
npm_network=
mx_host=
repo=lsh-okok/ok-email-scripts
ref=main
assume_yes=false
fresh_install=false
http_port_arg=
dir_set=false
project_set=false
mode_set=false
need_value() { [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "Missing value for $1"; }
while (($#)); do
    case $1 in
        -h|--help) usage; exit 0 ;;
        --yes) assume_yes=true; shift ;;
        --fresh) fresh_install=true; shift ;;
        --port) need_value "$@"; http_port_arg=$2; shift 2 ;;
        --dir) need_value "$@"; install_dir=$2; dir_set=true; shift 2 ;;
        --project) need_value "$@"; project=$2; project_set=true; shift 2 ;;
        --source) need_value "$@"; source_arg=$2; shift 2 ;;
        --mode) need_value "$@"; mode=$2; mode_set=true; shift 2 ;;
        --host) need_value "$@"; public_host=$2; shift 2 ;;
        --domain) need_value "$@"; domain=$2; shift 2 ;;
        --npm-network) need_value "$@"; npm_network=$2; shift 2 ;;
        --mx-host) need_value "$@"; mx_host=$2; shift 2 ;;
        --repo) need_value "$@"; repo=$2; shift 2 ;;
        --ref) need_value "$@"; ref=$2; shift 2 ;;
        *) die "Unknown option: $1 (use --help)" ;;
    esac
done

[[ $(uname -s) == Linux ]] || die 'Run this script on your Linux server.'
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
script_path="$script_dir/$(basename -- "${BASH_SOURCE[0]}")"
if [[ $(id -u) != 0 ]]; then
    command -v sudo >/dev/null || die 'Run as root or install sudo.'
    exec sudo bash "$script_path" "${original_args[@]}"
fi
interactive=false
if [[ $assume_yes != true && -t 0 ]]; then interactive=true; fi
prompt() {
    local variable=$1 label=$2 fallback=${3:-} answer
    read -r -p "$label [$fallback]: " answer || die 'Input ended; installation cancelled.'
    printf -v "$variable" '%s' "${answer:-$fallback}"
}

if [[ $fresh_install == true ]]; then
    fresh_id="$(date -u +%Y%m%d-%H%M%S)-$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
    if [[ $dir_set != true ]]; then install_dir="/opt/mail-lite-$fresh_id"; fi
    if [[ $project_set != true ]]; then project="mail-lite-$fresh_id"; fi
fi
[[ $install_dir =~ ^/[a-zA-Z0-9_./-]+$ && ! $install_dir =~ (^|/)\.\.(/|$) ]] || die 'Use an absolute installation path without spaces or .. components.'
install_dir=$(readlink -m -- "$install_dir")
[[ $install_dir != / ]] || die 'The installation directory cannot be /.'

if [[ $fresh_install == true && -e $install_dir ]]; then
    [[ -d $install_dir && -z $(ls -A -- "$install_dir") ]] || die 'Fresh installation requires a new or empty --dir; existing files will not be reused.'
fi
if [[ -f $install_dir/.deploy/project ]]; then
    saved_project=$(cat "$install_dir/.deploy/project")
    [[ $project_set != true || $project == "$saved_project" ]] || die "Existing installation uses project $saved_project. Keep that project name to retain its data volume."
    project=$saved_project
fi
[[ $project =~ ^[a-z0-9][a-z0-9_-]*$ ]] || die 'Invalid Compose project name.'

configure_proxy=false
configure_mx=false
if [[ $mode_set == true || -n $domain ]]; then configure_proxy=true; fi
if [[ $mode_set != true && -n $domain ]]; then mode=caddy; fi
if [[ -n $mx_host ]]; then configure_mx=true; fi
[[ $mode == direct || $mode == caddy || $mode == npm ]] || die 'Mode must be direct, caddy or npm.'
[[ $mode == direct || -n $domain ]] || die 'Set --domain for caddy/npm mode.'
[[ $mode != direct || -z $domain ]] || die 'Use --mode caddy/npm to configure a domain; direct mode uses the server IP.'
for hostname_value in "$domain" "$mx_host"; do
    [[ -z $hostname_value || $hostname_value =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]+$ ]] || die 'Invalid domain/MX hostname; omit the scheme, path and port.'
done
[[ -z $public_host || $public_host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]] || die 'Invalid public host; use an IPv4 address or hostname without a scheme or port.'
[[ -z $npm_network || $npm_network =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || die 'Invalid NPM network name.'
[[ $repo =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ ]] || die 'Invalid GitHub repository; use OWNER/REPO.'
[[ $ref =~ ^[a-zA-Z0-9][a-zA-Z0-9._/-]*$ && $ref != *..* ]] || die 'Invalid GitHub ref.'

action=deploy
if [[ -f $install_dir/.env ]]; then
    [[ -z $http_port_arg ]] || die '--port applies to new installations. Use --fresh for a separate instance.'
    action=update
    printf 'Reusing %s/.env and Compose project %s.\n' "$install_dir" "$project"
    if [[ $configure_proxy == true || $configure_mx == true ]]; then
        printf 'Explicit proxy/MX options will update the configuration with a backup.\n'
    fi
fi

source_dir=
if [[ -n $source_arg ]]; then
    [[ -d $source_arg ]] || die "Source directory not found: $source_arg"
    source_dir=$(cd -- "$source_arg" && pwd -P)
    if [[ -f $source_dir/mail-lite/Dockerfile ]]; then source_dir="$source_dir/mail-lite"; fi
elif [[ -f $script_dir/mail-lite/Dockerfile ]]; then
    source_dir="$script_dir/mail-lite"
elif [[ -f $script_dir/Dockerfile && -f $script_dir/go.mod ]]; then
    source_dir=$script_dir
fi

# Docker itself is installed by server-deploy.sh after the source is prepared.
missing_tools=false
required_tools=(curl tar gzip find flock)
if [[ $fresh_install == true ]]; then required_tools+=(ss); fi
for program in "${required_tools[@]}"; do
    if ! command -v "$program" >/dev/null; then missing_tools=true; fi
done
if [[ $missing_tools == true ]]; then
    [[ -f /etc/os-release ]] || die 'Install curl, tar, gzip, findutils and util-linux first.'
    . /etc/os-release
    [[ $ID == ubuntu || $ID == debian ]] || die 'Automatic dependency installation supports Ubuntu/Debian only.'
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    packages=(ca-certificates curl tar gzip findutils util-linux)
    if [[ $fresh_install == true ]]; then packages+=(iproute2); fi
    apt-get install -y "${packages[@]}"
fi

http_port=${http_port_arg:-8080}
[[ $http_port =~ ^[0-9]{1,5}$ ]] || die 'HTTP port must be a number from 1 to 65535.'
http_port=$((10#$http_port))
((http_port >= 1 && http_port <= 65535)) || die 'HTTP port must be a number from 1 to 65535.'
if [[ $fresh_install == true ]]; then
    reserved_ports=
    if command -v docker >/dev/null; then
        if ! docker info >/dev/null 2>&1; then
            if command -v systemctl >/dev/null; then systemctl start docker; fi
            docker info >/dev/null 2>&1 || die 'Start Docker so existing container ports can be checked.'
        fi
        container_ids=$(docker ps -aq) || die 'Could not inspect existing Docker containers.'
        if [[ -n $container_ids ]]; then
            mapfile -t existing_containers <<< "$container_ids"
            reserved_ports=$(docker inspect --format '{{range .HostConfig.PortBindings}}{{range .}}{{println .HostPort}}{{end}}{{end}}' "${existing_containers[@]}") || die 'Could not inspect existing Docker port bindings.'
        fi
    fi
    port_available() {
        local listeners reserved_port
        listeners=$(ss -H -ltn "sport = :$1") || die 'Could not inspect listening TCP ports.'
        [[ -z $listeners ]] || return 1
        while IFS= read -r reserved_port; do
            [[ $reserved_port != "$1" ]] || return 1
        done <<< "$reserved_ports"
        return 0
    }
    if [[ -n $http_port_arg ]]; then
        port_available "$http_port" || die "HTTP port $http_port is already in use; select another --port."
    else
        while ! port_available "$http_port"; do
            ((http_port < 8180)) || die 'No free HTTP port from 8080 to 8180. Supply --port explicitly.'
            http_port=$((http_port + 1))
        done
    fi
    printf 'Fresh instance: %s; project %s; HTTP port %s.\n' "$install_dir" "$project" "$http_port"
fi

is_public_ipv4() {
    local address=$1 a b c d octet
    [[ $address =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || return 1
    IFS=. read -r a b c d <<< "$address"
    for octet in "$a" "$b" "$c" "$d"; do
        [[ $octet == 0 || $octet != 0* ]] || return 1
        ((10#$octet <= 255)) || return 1
    done
    ((a > 0 && a < 224 && a != 10 && a != 127)) || return 1
    ! ((a == 100 && b >= 64 && b <= 127 || a == 169 && b == 254 || a == 172 && b >= 16 && b <= 31 || a == 192 && (b == 168 || b == 0 && (c == 0 || c == 2) || b == 88 && c == 99) || a == 198 && (b == 18 || b == 19 || b == 51 && c == 100) || a == 203 && b == 0 && c == 113))
}
detect_public_ipv4() {
    local candidate endpoint ssh_server
    if [[ -n ${SSH_CONNECTION:-} ]]; then
        read -r _ _ ssh_server _ <<< "$SSH_CONNECTION"
        if is_public_ipv4 "$ssh_server"; then printf '%s' "$ssh_server"; return; fi
    fi
    for endpoint in https://api.ipify.org https://checkip.amazonaws.com; do
        if candidate=$(curl -4 -fsS --noproxy '*' --connect-timeout 2 --max-time 5 "$endpoint" 2>/dev/null); then
            candidate="${candidate#"${candidate%%[![:space:]]*}"}"
            candidate="${candidate%"${candidate##*[![:space:]]}"}"
            if is_public_ipv4 "$candidate"; then printf '%s' "$candidate"; return; fi
        fi
    done
    return 1
}
if [[ -z $public_host ]]; then
    if [[ $action == update && $configure_proxy != true ]]; then
        # The deployment engine reads the configured URL during ordinary updates.
        public_host=127.0.0.1
    elif [[ $mode != direct ]]; then
        public_host=$domain
    else
        printf 'Detecting server public IPv4...\n'
        if public_host=$(detect_public_ipv4); then
            printf 'Detected public IPv4: %s\n' "$public_host"
        elif [[ $interactive == true ]]; then
            prompt public_host 'Automatic detection failed. Enter the server IP or hostname' ''
        else
            die 'Could not detect a public IPv4. Retry with --host YOUR_SERVER_IP.'
        fi
    fi
fi
[[ -n $public_host && $public_host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]] || die 'Invalid server IP/hostname; supply --host YOUR_SERVER_IP.'

work_dir=$(mktemp -d /tmp/mail-lite-install.XXXXXX)
cleanup() {
    # Only remove the private temporary directory created by this invocation.
    case $work_dir in /tmp/mail-lite-install.*) rm -rf -- "$work_dir" ;; esac
}
trap cleanup EXIT
trap 'printf "Installation failed. Fix the error above and rerun the installer.\n" >&2' ERR
if [[ -z $source_dir ]]; then
    printf 'Downloading %s (%s)...\n' "$repo" "$ref"
    curl -fL --retry 3 --connect-timeout 15 "https://codeload.github.com/$repo/tar.gz/$ref" -o "$work_dir/repository.tar.gz"
    mkdir "$work_dir/checkout"
    tar -xzf "$work_dir/repository.tar.gz" -C "$work_dir/checkout" --strip-components=1 --no-same-owner
    source_dir="$work_dir/checkout/mail-lite"
fi
for required_file in Dockerfile compose.yml compose.https.yml compose.npm.yml compose.mx.yml go.mod go.sum scripts/server-deploy.sh; do
    [[ -f $source_dir/$required_file && ! -L $source_dir/$required_file ]] || die "Missing $required_file in $source_dir. Upload the current project or choose a GitHub --ref containing the Go deployment files."
done
printf 'Packaging current source: %s\n' "$source_dir"

# Match the Windows uploader: include build/source files, never runtime data,
# private environment files, ordinary mailbox TXT/CSV files or symlink targets.
include_file() {
    local relative=$1 name=${1##*/} lower
    lower=${name,,}
    case $lower in
        .env*) [[ $name == .env.example ]]; return ;;
        *.pem|*.key|*.p12|*.pfx|*.ppk|*.der|id_rsa*|id_ed25519*) return 1 ;;
        .*) [[ $name == .dockerignore || $name == .gitignore ]]; return ;;
    esac
    case $name in
        Dockerfile|Dockerfile.*|Caddyfile|Makefile|go.mod|go.sum|go.work|go.work.sum|yarn.lock|CMakeLists.txt|LICENSE|LICENSE.*|NOTICE|NOTICE.*|COPYING|COPYING.*|requirements*.txt) return 0 ;;
    esac
    case $lower in
        *.go|*.js|*.mjs|*.cjs|*.ts|*.tsx|*.jsx|*.vue|*.css|*.scss|*.sass|*.less|*.html|*.htm|*.json|*.yaml|*.yml|*.toml|*.xml|*.sql|*.graphql|*.proto|*.md|*.rst|*.sh|*.ps1|*.bat|*.svg|*.png|*.jpg|*.jpeg|*.gif|*.webp|*.avif|*.ico|*.woff|*.woff2|*.ttf|*.otf|*.eot|*.wasm|*.map) return 0 ;;
    esac
    return 1
}
file_list="$work_dir/files"
: > "$file_list"
for entry in "$source_dir/"* "$source_dir/.dockerignore" "$source_dir/.gitignore" "$source_dir/.env.example"; do
    [[ -f $entry && ! -L $entry ]] || continue
    name=${entry##*/}
    case $name in
        Dockerfile|Dockerfile.*|.dockerignore|.gitignore|.env.example|go.mod|go.sum|go.work|go.work.sum|Makefile|CMakeLists.txt|package.json|package-lock.json|npm-shrinkwrap.json|pnpm-lock.yaml|yarn.lock|compose*.yml|compose*.yaml|*.md|requirements*.txt|LICENSE|LICENSE.*|NOTICE|NOTICE.*|COPYING|COPYING.*)
            if include_file "$name"; then printf './%s\0' "$name" >> "$file_list"; fi ;;
    esac
done
for source_root in cmd internal web scripts deploy docs examples; do
    [[ -d $source_dir/$source_root && ! -L $source_dir/$source_root ]] || continue
    find "$source_dir/$source_root" \
        \( -type d \( -name '.*' -o -name node_modules -o -name bin -o -name data -o -name imports -o -name exports -o -path "$source_dir/web/ui/dist" \) -prune \) -o -type f -print0 > "$work_dir/candidates"
    while IFS= read -r -d '' entry; do
        relative=${entry#"$source_dir/"}
        if include_file "$relative"; then printf './%s\0' "$relative" >> "$file_list"; fi
    done < "$work_dir/candidates"
done
tar -czf "$work_dir/source.tar.gz" -C "$source_dir" --no-recursion --null -T "$file_list"
sed 's/\r$//' "$source_dir/scripts/server-deploy.sh" > "$work_dir/server-deploy.sh"
enable_mx=false
if [[ -n $mx_host ]]; then enable_mx=true; fi
printf 'Deploying to %s (project %s).\n' "$install_dir" "$project"
bash "$work_dir/server-deploy.sh" "$work_dir/source.tar.gz" "$install_dir" "$project" \
    "$public_host" "$domain" "$enable_mx" "$mx_host" "$action" "$mode" "$npm_network" \
    "$configure_proxy" "$configure_mx" "$http_port" "$fresh_install"
