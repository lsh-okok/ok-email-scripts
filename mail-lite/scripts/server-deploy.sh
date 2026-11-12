#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[[ $# == 8 || $# == 10 ]] || die 'Usage: server-deploy.sh ARCHIVE ROOT PROJECT PUBLIC_HOST DOMAIN MX_ENABLED MX_HOST deploy|update [npm|caddy|direct NPM_NETWORK]'
archive=$1
root=${2%/}
project=$3
public_host=$4
domain=$5
enable_mx=$6
mx_host=$7
action=$8
proxy_mode_arg=${9:-}
npm_network_arg=${10:-}
if [[ -z $proxy_mode_arg ]]; then
    if [[ -n $domain ]]; then proxy_mode_arg=caddy; else proxy_mode_arg=direct; fi
fi
[[ $root =~ ^/[a-zA-Z0-9_./-]+$ && $root != / && ! $root =~ (^|/)\.\.(/|$) ]] || die 'Invalid installation directory.'
[[ $project =~ ^[a-z0-9][a-z0-9_-]*$ ]] || die 'Invalid Compose project name.'
[[ $public_host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*$ ]] || die 'Invalid public hostname.'
[[ -z $domain || $domain =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]+$ ]] || die 'Invalid website domain.'
[[ -z $mx_host || $mx_host =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]+$ ]] || die 'Invalid MX hostname.'
[[ $enable_mx == true || $enable_mx == false ]] || die 'Invalid MX flag.'
[[ $action == deploy || $action == update ]] || die 'Invalid action.'
[[ $proxy_mode_arg == npm || $proxy_mode_arg == caddy || $proxy_mode_arg == direct ]] || die 'Invalid proxy mode.'
[[ -z $npm_network_arg || $npm_network_arg =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || die 'Invalid NPM network name.'
[[ -f $archive ]] || die "Package not found: $archive"

if [[ $(id -u) != 0 ]]; then
    command -v sudo >/dev/null || die 'Use root or install and configure passwordless sudo.'
    exec sudo -n bash "$0" "$@"
fi

state="$root/.deploy"
[[ $action != update || -f $root/.env ]] || die 'No server .env found. Run install-mail-lite.sh or deploy.bat first.'
mkdir -p "$state/releases" "$state/backups"
chmod 700 "$state"
exec 9>"$state/deploy.lock"
flock -n 9 || die 'Another deployment is running.'
# Keep the volume identity even if an initial build or readiness check fails.
if [[ -f $state/project ]]; then
    [[ $(cat "$state/project") == "$project" ]] || die 'Compose project differs from the saved installation; keep the original project name.'
fi
printf '%s\n' "$project" > "$state/project"
old_container=
resume_old=false
backup=
compose=()
on_failure() {
    local result=$?
    trap - ERR
    if [[ $resume_old == true && -n $old_container ]]; then
        docker start "$old_container" >/dev/null 2>&1 || true
    fi
    printf '\nDeployment failed. Existing data and .env were retained.\n' >&2
    if [[ -n $backup ]]; then printf 'Backup: %s\n' "$backup" >&2; fi
    if ((${#compose[@]})); then "${compose[@]}" logs --tail=60 mail >&2 || true; fi
    exit "$result"
}
trap on_failure ERR
trap 'false' HUP INT TERM

if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
    # The official Docker installer configures its Ubuntu/Debian package repository.
    . /etc/os-release
    [[ $ID == ubuntu || $ID == debian ]] || die 'Automatic Docker installation supports Ubuntu and Debian only.'
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y ca-certificates curl
    installer=$(mktemp "$state/docker-install.XXXXXX.sh")
    curl -fsSL --retry 3 https://get.docker.com -o "$installer"
    sh "$installer"
    rm -f -- "$installer"
fi
if command -v systemctl >/dev/null; then systemctl enable --now docker; fi
docker info >/dev/null
docker compose version >/dev/null

# Read only simple deployment settings. Never execute the contents of .env.
read_setting() {
    local key=$1 line value=
    while IFS= read -r line || [[ -n $line ]]; do
        line=${line%$'\r'}
        if [[ $line =~ ^[[:space:]]*$key[[:space:]]*=(.*)$ ]]; then value=${BASH_REMATCH[1]}; fi
    done < "$root/.env"
    value="${value#"${value%%[![:space:]]*}"}"
    if [[ $value == \"*\" ]]; then value=${value:1:${#value}-2};
    elif [[ $value == \'*\' ]]; then value=${value:1:${#value}-2};
    else value=${value%%[[:space:]]#*}; fi
    value="${value%"${value##*[![:space:]]}"}"
    printf '%s' "$value"
}

initial_password=
if [[ ! -f $root/.env ]]; then
    [[ $action == deploy ]] || die 'No server .env found. Run install-mail-lite.sh or deploy.bat first.'
    [[ $enable_mx != true || -n $mx_host ]] || die 'MX hostname is required.'
    [[ $proxy_mode_arg == direct || -n $domain ]] || die 'A website domain is required for npm/caddy mode.'
    initial_password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
    if [[ $proxy_mode_arg != direct ]]; then origin="https://$domain"; bind=127.0.0.1;
    else origin="http://${domain:-$public_host}:8080"; bind=0.0.0.0; fi
    cat > "$root/.env" <<EOF
ADMIN_PASSWORD=$initial_password
APP_ORIGIN=$origin
HTTP_BIND=$bind
HTTP_PORT=8080
MAIL_DOMAIN=$domain
PROXY_MODE=$proxy_mode_arg
NPM_NETWORK=$npm_network_arg
NPM_UPSTREAM=${project//_/-}-app
UPSTREAM_CONCURRENCY=16
SMTP_ENABLED=$enable_mx
SMTP_HOSTNAME=$mx_host
SMTP_TLS_CERT=
SMTP_TLS_KEY=
EOF
fi
chmod 600 "$root/.env"
if [[ -n $initial_password ]]; then printf 'Initial administrator password: %s\n' "$initial_password"; fi
# Server settings win on every update, including MX and the administrator password.
domain=$(read_setting MAIL_DOMAIN)
enable_mx=$(read_setting SMTP_ENABLED)
origin=$(read_setting APP_ORIGIN)
proxy_mode=$(read_setting PROXY_MODE)
npm_network=$(read_setting NPM_NETWORK)
npm_upstream=$(read_setting NPM_UPSTREAM)
npm_upstream=${npm_upstream:-${project//_/-}-app}
if [[ -z $proxy_mode ]]; then
    if [[ -n $domain ]]; then proxy_mode=caddy; else proxy_mode=direct; fi
fi
http_port=$(read_setting HTTP_PORT)
http_port=${http_port:-8080}
[[ -z $domain || $domain =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]+$ ]] || die 'Invalid MAIL_DOMAIN in .env.'
[[ -z $enable_mx || $enable_mx == true || $enable_mx == false ]] || die 'SMTP_ENABLED must be true or false.'
[[ $http_port =~ ^[0-9]+$ && $http_port -ge 1 && $http_port -le 65535 ]] || die 'Invalid HTTP_PORT in .env.'
[[ $proxy_mode == npm || $proxy_mode == caddy || $proxy_mode == direct ]] || die 'Invalid PROXY_MODE in .env.'
if [[ $proxy_mode == npm ]]; then
    [[ $origin == https://* ]] || die 'NPM mode requires APP_ORIGIN=https://your-website-domain in .env.'
    [[ $npm_upstream =~ ^[a-z0-9][a-z0-9-]*$ ]] || die 'Invalid NPM_UPSTREAM in .env.'
    if [[ -z $npm_network ]]; then
        # Reuse an NPM network so connectivity also survives NPM container recreation.
        mapfile -t npm_containers < <(docker ps --format '{{.ID}} {{.Image}}' | awk '$2 ~ /nginx-proxy-manager/ {print $1}')
        ((${#npm_containers[@]} == 1)) || die 'Cannot identify a single NPM container. Set NPM_NETWORK in the server .env to its Docker network name, then retry.'
        mapfile -t npm_networks < <(docker inspect --format '{{range $name, $network := .NetworkSettings.Networks}}{{println $name}}{{end}}' "${npm_containers[0]}" | sed '/^$/d; /^bridge$/d; /^host$/d; /^none$/d')
        if ((${#npm_networks[@]} == 1)); then npm_network=${npm_networks[0]};
        else die "NPM uses multiple/no user-defined networks (${npm_networks[*]}). Set NPM_NETWORK in the server .env, then retry."; fi
        printf '\nNPM_NETWORK=%s\n' "$npm_network" >> "$root/.env"
    fi
    [[ $npm_network =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ && $npm_network != bridge && $npm_network != host && $npm_network != none ]] || die 'NPM_NETWORK must be a user-defined Docker bridge network.'
    [[ $(docker network inspect --format '{{.Driver}}' "$npm_network") == bridge ]] || die 'NPM_NETWORK must exist on this server and use the bridge driver.'
    export NPM_UPSTREAM="$npm_upstream"
    printf 'Using Nginx Proxy Manager network %s; upstream http://%s:8080\n' "$npm_network" "$npm_upstream"
fi

release_id="$(date -u +%Y%m%d-%H%M%S)-${RANDOM}${RANDOM}"
release="$state/releases/$release_id"
mkdir -p "$release"
tar -xzf "$archive" -C "$release" --no-same-owner
[[ -f $release/compose.yml && -f $release/Dockerfile && -f $release/go.mod ]] || die 'Incomplete deployment package.'
export MAIL_IMAGE="mail-lite:$release_id"
compose=(docker compose --project-name "$project" --project-directory "$release" --env-file "$root/.env" -f "$release/compose.yml")
if [[ $enable_mx == true ]]; then compose+=(-f "$release/compose.mx.yml"); fi
if [[ $proxy_mode == caddy ]]; then compose+=(-f "$release/compose.https.yml"); fi
if [[ $proxy_mode == npm ]]; then compose+=(-f "$release/compose.npm.yml"); fi
"${compose[@]}" config --quiet
printf '\nBuilding %s. The existing application stays running during the build.\n' "$MAIL_IMAGE"
"${compose[@]}" build mail
if [[ $proxy_mode == caddy ]]; then "${compose[@]}" pull caddy; fi

old_container=$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter label=com.docker.compose.service=mail)
[[ $old_container != *$'\n'* ]] || die 'More than one primary container found; check the project name.'
backup="$state/backups/$release_id"
mkdir -p "$backup"
cp "$root/.env" "$backup/env"
if [[ -L $state/current ]]; then readlink "$state/current" > "$backup/previous-release"; fi
if [[ -n $old_container ]]; then
    docker inspect --format '{{.Image}}' "$old_container" > "$backup/previous-image"
    resume_old=true
    docker stop --time 30 "$old_container" >/dev/null
fi
if [[ -n $old_container ]] || docker volume inspect "${project}_primary-data" >/dev/null 2>&1; then
    printf 'Saving a consistent backup of the complete data directory...\n'
    if [[ -n $old_container ]]; then mounts=(--volumes-from "$old_container:ro");
    else mounts=(--mount "type=volume,src=${project}_primary-data,dst=/data,readonly"); fi
    docker run --rm --user 0:0 "${mounts[@]}" \
        --mount "type=bind,src=$backup,dst=/backup" --entrypoint /bin/sh "$MAIL_IMAGE" \
        -c 'umask 077; tar -czf /backup/data.tar.gz -C /data .'
fi

# At this point a previous container may be replaced. Failure is reported with its
# backup path; data is never automatically rewound after accepting new mail.
resume_old=false
"${compose[@]}" up -d --no-build mail
container=$("${compose[@]}" ps -q mail)
ready=false
for ((attempt=0; attempt<60; attempt++)); do
    status=$(docker inspect --format '{{.State.Health.Status}}' "$container")
    if [[ $status == healthy ]]; then ready=true; break; fi
    running=$(docker inspect --format '{{.State.Running}}' "$container")
    [[ $running == true ]] || break
    sleep 2
done
if [[ $ready != true ]]; then
    printf 'Readiness check failed. Backup: %s\n' "$backup" >&2
    false
fi
if [[ $proxy_mode == caddy ]]; then "${compose[@]}" up -d --no-build caddy; fi
ln -sfn "$release" "$state/current"
printf '%s\n' "$MAIL_IMAGE" > "$state/current-image"
printf '%s\n' "$project" > "$state/project"
rm -f -- "$archive"

if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
    if [[ $proxy_mode == caddy ]]; then ufw allow 80/tcp; ufw allow 443/tcp;
    elif [[ $proxy_mode == direct ]]; then ufw allow "$http_port/tcp"; fi
    if [[ $enable_mx == true ]]; then ufw allow 25/tcp; fi
fi
printf '\nApplication ready: %s/admin/\n' "${origin:-http://$public_host:$http_port}"
printf 'Backup directory: %s\n' "$backup"
printf 'Server configuration: %s/.env\n' "$root"
if [[ $proxy_mode == caddy ]]; then printf 'HTTPS requires this domain to resolve to this server and public TCP 80/443 to be open.\n'; fi
if [[ $proxy_mode == npm ]]; then printf 'NPM Proxy Host: scheme=http, hostname=%s, port=8080. Configure the domain and HTTPS certificate in NPM.\n' "$npm_upstream"; fi
if [[ $enable_mx == true ]]; then printf 'MX receiving requires DNS A/MX records and public inbound TCP 25.\n'; fi
