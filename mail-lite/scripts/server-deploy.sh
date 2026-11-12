#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[[ $# == 8 || $# == 10 || $# == 12 || $# == 14 ]] || die 'Usage: server-deploy.sh ARCHIVE ROOT PROJECT PUBLIC_HOST DOMAIN MX_ENABLED MX_HOST deploy|update [npm|caddy|direct NPM_NETWORK [CONFIGURE_PROXY CONFIGURE_MX [NEW_HTTP_PORT FRESH_INSTALL]]]'
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
configure_proxy=${11:-false}
configure_mx=${12:-false}
new_http_port=${13:-8080}
fresh_install=${14:-false}
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
[[ $configure_proxy == true || $configure_proxy == false ]] || die 'Invalid proxy configuration flag.'
[[ $configure_mx == true || $configure_mx == false ]] || die 'Invalid MX configuration flag.'
[[ $fresh_install == true || $fresh_install == false ]] || die 'Invalid fresh installation flag.'
[[ $new_http_port =~ ^[0-9]{1,5}$ ]] || die 'Invalid new HTTP port.'
new_http_port=$((10#$new_http_port))
[[ $new_http_port -ge 1 && $new_http_port -le 65535 ]] || die 'Invalid new HTTP port.'
[[ $fresh_install != true || $action == deploy ]] || die 'Fresh installation requires a new deployment.'
if [[ $configure_proxy == true && $proxy_mode_arg != direct ]]; then
    [[ -n $domain ]] || die 'A website domain is required for npm/caddy mode.'
fi
if [[ $configure_mx == true && $enable_mx == true ]]; then
    [[ -n $mx_host ]] || die 'MX hostname is required.'
fi
[[ -f $archive ]] || die "Package not found: $archive"

if [[ $(id -u) != 0 ]]; then
    command -v sudo >/dev/null || die 'Use root or install and configure passwordless sudo.'
    exec sudo -n bash "$0" "$@"
fi

state="$root/.deploy"
check_fresh_directory() {
    [[ ! -e $root/.env && ! -L $root/.env && ! -e $state/project && ! -L $state/project && ! -e $state/current && ! -L $state/current ]] || die 'Fresh installation cannot reuse an existing installation directory. Choose a new directory and project.'
}
if [[ $fresh_install == true ]]; then check_fresh_directory; fi
[[ $action != update || -f $root/.env ]] || die 'No server .env found. Run install-mail-lite.sh or deploy.bat first.'
# Serialize the same Compose project even when callers choose different roots.
# A private, owned directory prevents following an attacker-supplied /tmp link.
project_locks=/tmp/mail-lite-deployment-locks
if ! mkdir -m 700 "$project_locks" 2>/dev/null; then
    [[ -d $project_locks && ! -L $project_locks && -O $project_locks ]] || die 'The project lock directory is not a private directory owned by this user.'
    chmod 700 "$project_locks"
fi
[[ ! -L $project_locks/$project.lock ]] || die 'Invalid project lock file.'
exec 8>"$project_locks/$project.lock"
flock -n 8 || die 'Another deployment of this Compose project is running.'
if [[ $fresh_install == true ]]; then check_fresh_directory; fi
mkdir -p "$state/releases" "$state/backups"
chmod 700 "$state"
exec 9>"$state/deploy.lock"
flock -n 9 || die 'Another deployment is running.'
# Keep the volume identity even if an initial build or readiness check fails.
if [[ -f $state/project ]]; then
    [[ $(cat "$state/project") == "$project" ]] || die 'Compose project differs from the saved installation; keep the original project name.'
fi
if [[ $fresh_install != true ]]; then printf '%s\n' "$project" > "$state/project"; fi
old_container=
resume_old=false
backup=
compose=()
stage_env=
configuration_committed=false
replacement_started=false
cleanup() {
    if [[ -n $stage_env && -f $stage_env ]]; then rm -f -- "$stage_env"; fi
}
on_failure() {
    local result=$?
    trap - ERR
    if [[ $resume_old == true && -n $old_container ]]; then
        docker start "$old_container" >/dev/null 2>&1 || true
    fi
    if [[ $configuration_committed == true && $replacement_started != true && -n $backup ]]; then
        cp "$backup/env" "$root/.env.restore"
        chmod 600 "$root/.env.restore"
        mv -f -- "$root/.env.restore" "$root/.env"
    fi
    printf '\nDeployment failed. Existing data was retained.\n' >&2
    if [[ -n $backup ]]; then printf 'Backup: %s\n' "$backup" >&2; fi
    if [[ $configuration_committed == true && $replacement_started == true ]]; then
        printf 'The server .env contains the requested settings for the replacement container; the previous settings are in the backup.\n' >&2
    fi
    if ((${#compose[@]})); then "${compose[@]}" logs --tail=60 mail >&2 || true; fi
    exit "$result"
}
trap on_failure ERR
trap 'false' HUP INT TERM
trap cleanup EXIT

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

if [[ $fresh_install == true ]]; then
    existing_project_containers=$(docker ps -aq --filter "label=com.docker.compose.project=$project")
    [[ -z $existing_project_containers ]] || die 'Fresh installation cannot reuse a Compose project with existing containers, including stopped containers. Choose a new project.'
    existing_project_volumes=$(docker volume ls -q --filter "label=com.docker.compose.project=$project")
    [[ -z $existing_project_volumes ]] || die 'Fresh installation cannot reuse a Compose project with existing volumes. Choose a new project.'
    if docker volume inspect "${project}_primary-data" >/dev/null 2>&1; then
        die 'Fresh installation cannot reuse an existing data volume. Choose a new project.'
    fi
    printf '%s\n' "$project" > "$state/project"
fi

settings_file="$root/.env"
# Read only simple deployment settings. Never execute the contents of .env.
read_setting() {
    local key=$1 line value=
    while IFS= read -r line || [[ -n $line ]]; do
        line=${line%$'\r'}
        if [[ $line =~ ^[[:space:]]*$key[[:space:]]*=(.*)$ ]]; then value=${BASH_REMATCH[1]}; fi
    done < "$settings_file"
    value="${value#"${value%%[![:space:]]*}"}"
    if [[ $value == \"*\" ]]; then value=${value:1:${#value}-2};
    elif [[ $value == \'*\' ]]; then value=${value:1:${#value}-2};
    else value=${value%%[[:space:]]#*}; fi
    value="${value%"${value##*[![:space:]]}"}"
    printf '%s' "$value"
}
write_setting() {
    local key=$1 value=$2 line
    local next_file="$stage_env.next"
    : > "$next_file"
    while IFS= read -r line || [[ -n $line ]]; do
        if [[ ! ${line%$'\r'} =~ ^[[:space:]]*$key[[:space:]]*= ]]; then
            printf '%s\n' "$line" >> "$next_file"
        fi
    done < "$stage_env"
    printf '%s=%s\n' "$key" "$value" >> "$next_file"
    chmod 600 "$next_file"
    mv -f -- "$next_file" "$stage_env"
}

initial_password=
existing_configuration=false
if [[ -f $root/.env ]]; then existing_configuration=true; fi
if [[ ! -f $root/.env ]]; then
    [[ $action == deploy ]] || die 'No server .env found. Run install-mail-lite.sh or deploy.bat first.'
    [[ $enable_mx != true || -n $mx_host ]] || die 'MX hostname is required.'
    [[ $proxy_mode_arg == direct || -n $domain ]] || die 'A website domain is required for npm/caddy mode.'
    initial_password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
    if [[ $proxy_mode_arg != direct ]]; then origin="https://$domain"; bind=127.0.0.1;
    else origin="http://${domain:-$public_host}:$new_http_port"; bind=0.0.0.0; fi
    cat > "$root/.env" <<EOF
ADMIN_PASSWORD=$initial_password
APP_ORIGIN=$origin
HTTP_BIND=$bind
HTTP_PORT=$new_http_port
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
if [[ $existing_configuration == true && ( $configure_proxy == true || $configure_mx == true ) ]]; then
    stage_env=$(mktemp "$state/settings.XXXXXX.env")
    cp "$root/.env" "$stage_env"
    settings_file=$stage_env
    if [[ $configure_proxy == true ]]; then
        configured_port=$(read_setting HTTP_PORT)
        configured_port=${configured_port:-8080}
        [[ $configured_port =~ ^[0-9]+$ && $configured_port -ge 1 && $configured_port -le 65535 ]] || die 'Invalid HTTP_PORT in .env.'
        if [[ $proxy_mode_arg == direct ]]; then
            write_setting APP_ORIGIN "http://$public_host:$configured_port"
            write_setting HTTP_BIND 0.0.0.0
            write_setting MAIL_DOMAIN ''
            write_setting NPM_NETWORK ''
        else
            write_setting APP_ORIGIN "https://$domain"
            write_setting HTTP_BIND 127.0.0.1
            write_setting MAIL_DOMAIN "$domain"
            write_setting NPM_NETWORK "$npm_network_arg"
        fi
        write_setting PROXY_MODE "$proxy_mode_arg"
    fi
    if [[ $configure_mx == true ]]; then
        write_setting SMTP_ENABLED "$enable_mx"
        write_setting SMTP_HOSTNAME "$mx_host"
    fi
fi
# Existing settings remain authoritative unless a setting was explicitly requested.
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
        printf '\nNPM_NETWORK=%s\n' "$npm_network" >> "$settings_file"
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
compose=(docker compose --project-name "$project" --project-directory "$release" --env-file "$settings_file" -f "$release/compose.yml")
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
if [[ -n $stage_env ]]; then
    cp "$stage_env" "$root/.env.new"
    chmod 600 "$root/.env.new"
    mv -f -- "$root/.env.new" "$root/.env"
    configuration_committed=true
fi
resume_old=false
replacement_started=true
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
if [[ $proxy_mode == caddy ]]; then
    "${compose[@]}" up -d --no-build caddy
else
    mapfile -t old_caddy_containers < <(docker ps -q --filter "label=com.docker.compose.project=$project" --filter label=com.docker.compose.service=caddy)
    if ((${#old_caddy_containers[@]})); then
        docker stop --time 30 "${old_caddy_containers[@]}" >/dev/null
    fi
fi
ln -sfn "$release" "$state/current"
printf '%s\n' "$MAIL_IMAGE" > "$state/current-image"
printf '%s\n' "$project" > "$state/project"
rm -f -- "$archive"

if command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then
    if [[ $proxy_mode == caddy ]]; then ufw allow 80/tcp; ufw allow 443/tcp;
    elif [[ $proxy_mode == direct ]]; then ufw allow "$http_port/tcp"; fi
    if [[ $enable_mx == true ]]; then ufw allow 25/tcp; fi
fi
printf '\nApplication is running; local readiness check passed.\n'
http_bind=$(read_setting HTTP_BIND)
http_bind=${http_bind:-127.0.0.1}
if [[ $proxy_mode == direct && $http_bind != 127.* && $http_bind != localhost && $http_bind != ::1 ]]; then
    printf 'Management URL: %s/admin/\n' "${origin:-http://$public_host:$http_port}"
    printf 'Allow inbound TCP %s in the server firewall and cloud security group.\n' "$http_port"
else
    printf 'Local management URL: http://127.0.0.1:%s/admin/\n' "$http_port"
    if [[ -n $origin ]]; then printf 'Configured domain URL (requires DNS/proxy/HTTPS setup): %s/admin/\n' "$origin"; fi
fi
printf 'Installation directory: %s\n' "$root"
printf 'Compose project: %s\n' "$project"
printf 'Backup directory: %s\n' "$backup"
printf 'Server configuration: %s/.env\n' "$root"
if [[ $proxy_mode == caddy ]]; then printf 'HTTPS requires this domain to resolve to this server and public TCP 80/443 to be open.\n'; fi
if [[ $proxy_mode == npm ]]; then printf 'NPM Proxy Host: scheme=http, hostname=%s, port=8080. Configure the domain and HTTPS certificate in NPM.\n' "$npm_upstream"; fi
if [[ $enable_mx == true ]]; then printf 'MX receiving requires DNS A/MX records and public inbound TCP 25.\n'; fi
if [[ -n $initial_password ]]; then
    printf '\nInitial administrator password: %s\n' "$initial_password"
    printf 'Use this password to sign in to the management URL above; you can change it in System Settings.\n'
fi
