#!/usr/bin/env bash
# Linux entry point; reuse the same deployment engine as the Windows uploader.
set -Eeuo pipefail
umask 077

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'HELP'
Mail Lite one-command installer (Ubuntu / Debian)

  sudo bash install-mail-lite.sh
  sudo bash install-mail-lite.sh --yes --host 203.0.113.10
  sudo bash install-mail-lite.sh --yes --mode caddy --domain mail.example.com

Options:
  --dir PATH          Installation directory (default: /opt/mail-lite)
  --project NAME      Compose project name (default: mail-lite; reused on update)
  --source PATH       Local repository or mail-lite source directory
  --mode MODE         direct (HTTP), caddy (automatic HTTPS), or npm
  --host HOST         Public IPv4 address or hostname for HTTP access
  --domain DOMAIN     Website domain; required for caddy/npm
  --npm-network NAME  Existing Nginx Proxy Manager Docker bridge network
  --mx-host HOST      Enable MX receiving with this public mail hostname
  --repo OWNER/REPO   GitHub source when no local source exists (lsh-okok/ok-email-scripts)
  --ref REF           GitHub branch/tag/commit (main)
  --yes               Use supplied options/defaults without prompting
  -h, --help          Show this help

Local source beside this script takes priority over GitHub. Existing server
.env settings and data are retained. Rerun with updated source to upgrade.
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
dir_set=false
project_set=false
mode_set=false
need_value() { [[ $# -ge 2 && -n $2 && $2 != --* ]] || die "Missing value for $1"; }
while (($#)); do
    case $1 in
        -h|--help) usage; exit 0 ;;
        --yes) assume_yes=true; shift ;;
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
if [[ $assume_yes != true ]]; then
    [[ -t 0 ]] || die 'No interactive terminal. Use --yes with --host or --domain.'
    interactive=true
fi
prompt() {
    local variable=$1 label=$2 fallback=${3:-} answer
    read -r -p "$label [$fallback]: " answer || die 'Input ended; installation cancelled.'
    printf -v "$variable" '%s' "${answer:-$fallback}"
}

if [[ $interactive == true && $dir_set != true ]]; then
    prompt install_dir 'Installation directory' "$install_dir"
fi
[[ $install_dir =~ ^/[a-zA-Z0-9_./-]+$ && ! $install_dir =~ (^|/)\.\.(/|$) ]] || die 'Use an absolute installation path without spaces or .. components.'
install_dir=$(readlink -m -- "$install_dir")
[[ $install_dir != / ]] || die 'The installation directory cannot be /.'

if [[ -f $install_dir/.deploy/project ]]; then
    saved_project=$(cat "$install_dir/.deploy/project")
    [[ $project_set != true || $project == "$saved_project" ]] || die "Existing installation uses project $saved_project. Keep that project name to retain its data volume."
    project=$saved_project
elif [[ $interactive == true && $project_set != true ]]; then
    prompt project 'Compose project name (keep the original name when upgrading)' "$project"
fi
[[ $project =~ ^[a-z0-9][a-z0-9_-]*$ ]] || die 'Invalid Compose project name.'

action=deploy
if [[ -f $install_dir/.env ]]; then
    action=update
    printf 'Reusing %s/.env and Compose project %s. To change proxy/MX settings, edit that file.\n' "$install_dir" "$project"
    public_host=${public_host:-127.0.0.1}
else
    if [[ $interactive == true ]]; then
        if [[ $mode_set != true ]]; then prompt mode 'Website mode: direct / caddy / npm' "$mode"; fi
        if [[ $mode == direct && -z $public_host && -z $domain ]]; then
            detected_host=
            if [[ -n ${SSH_CONNECTION:-} ]]; then
                read -r _ _ detected_host _ <<< "$SSH_CONNECTION"
            fi
            prompt public_host 'Public IPv4 address or hostname (use your external address)' "$detected_host"
        elif [[ $mode != direct && -z $domain ]]; then
            prompt domain 'Website domain, without https://' ''
        fi
        if [[ $mode == npm && -z $npm_network ]]; then
            prompt npm_network 'Existing NPM Docker network (empty = auto-detect)' ''
        fi
        if [[ -z $mx_host ]]; then
            prompt mx_answer 'Enable MX receiving? y / n' n
            case $mx_answer in
                y|Y|yes|YES) prompt mx_host 'Public MX hostname, for example mx.example.com' ''; [[ -n $mx_host ]] || die 'MX hostname is required.' ;;
                n|N|no|NO) ;;
                *) die 'Choose y or n for MX receiving.' ;;
            esac
        fi
    fi
    [[ $mode == direct || -n $domain ]] || die 'Set --domain for caddy/npm mode.'
    public_host=${public_host:-$domain}
    [[ -n $public_host ]] || die 'Set --host to your server public IPv4 address or hostname.'
fi
[[ $mode == direct || $mode == caddy || $mode == npm ]] || die 'Mode must be direct, caddy or npm.'
[[ $public_host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]] || die 'Invalid public host; use an IPv4 address or hostname without a scheme or port.'
for hostname_value in "$domain" "$mx_host"; do
    [[ -z $hostname_value || $hostname_value =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]+$ ]] || die 'Invalid domain/MX hostname; omit the scheme, path and port.'
done
[[ -z $npm_network || $npm_network =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || die 'Invalid NPM network name.'
[[ $repo =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ ]] || die 'Invalid GitHub repository; use OWNER/REPO.'
[[ $ref =~ ^[a-zA-Z0-9][a-zA-Z0-9._/-]*$ && $ref != *..* ]] || die 'Invalid GitHub ref.'

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
for program in curl tar gzip find flock; do
    if ! command -v "$program" >/dev/null; then missing_tools=true; fi
done
if [[ $missing_tools == true ]]; then
    [[ -f /etc/os-release ]] || die 'Install curl, tar, gzip, findutils and util-linux first.'
    . /etc/os-release
    [[ $ID == ubuntu || $ID == debian ]] || die 'Automatic dependency installation supports Ubuntu/Debian only.'
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y ca-certificates curl tar gzip findutils util-linux
fi

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
    "$public_host" "$domain" "$enable_mx" "$mx_host" "$action" "$mode" "$npm_network"
