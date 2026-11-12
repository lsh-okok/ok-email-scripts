#!/usr/bin/env bash
# Exercise deployment sequencing and real tar backups without a Docker daemon.
set -Eeuo pipefail
scripts=$(cd "$(dirname "$0")/.." && pwd)
fixtures=$(mktemp -d /tmp/mail-lite-deploy-test.XXXXXX)
mkdir -p "$fixtures/bin" "$fixtures/source" "$fixtures/data"
printf 'fixture database\n' > "$fixtures/data/mail-lite.db"
printf 'fixture mx database\n' > "$fixtures/data/mx-mail.db"
printf 'fixture key\n' > "$fixtures/data/data.key"
printf 'fixture WAL\n' > "$fixtures/data/mail-lite.db-wal"
for file in compose.yml compose.mx.yml compose.https.yml compose.npm.yml Dockerfile go.mod; do
    printf 'fixture\n' > "$fixtures/source/$file"
done
tar -czf "$fixtures/package.tar.gz" -C "$fixtures/source" .
printf '#!/usr/bin/env bash\nprintf "0\\n"\n' > "$fixtures/bin/id"
printf '#!/usr/bin/env bash\nexit 0\n' > "$fixtures/bin/flock"
cat > "$fixtures/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -eu
printf '%s|' "$@" >> "$MOCK_CASE/commands"
printf '\n' >> "$MOCK_CASE/commands"
if [[ $1 == compose ]]; then
    shift
    while (($#)); do
        case $1 in
            --project-name|--project-directory|--env-file|-f) shift 2 ;;
            *) break ;;
        esac
    done
    case ${1:-} in
        build) [[ $MOCK_SCENARIO != build-failure ]] ;;
        ps) printf 'new-container\n' ;;
        *) exit 0 ;;
    esac
elif [[ $1 == ps ]]; then
    if [[ ${2:-} == --format ]]; then printf 'npm-container jc21/nginx-proxy-manager:latest\n';
    elif [[ ${2:-} == -aq ]]; then
        if [[ ${MOCK_FRESH_CONTAINERS:-false} == true ]]; then printf 'stopped-old-container\n'; fi
    elif [[ $* == *com.docker.compose.service=caddy* ]]; then
        if [[ ${MOCK_OLD_CADDY:-false} == true ]]; then printf 'old-caddy\n'; fi
    elif [[ $MOCK_SCENARIO != first ]]; then printf 'old-container\n'; fi
elif [[ $1 == network ]]; then
    [[ ${5:-} != missing-network ]] || exit 1
    printf 'bridge\n'
elif [[ $1 == volume ]]; then
    if [[ ${2:-} == ls ]]; then
        if [[ ${MOCK_FRESH_OTHER_VOLUME:-false} == true ]]; then printf 'fresh-mail_caddy-data\n'; fi
    else
        [[ ${MOCK_FRESH_VOLUME:-false} == true || $MOCK_SCENARIO != first ]]
    fi
elif [[ $1 == stop ]]; then
    touch "$MOCK_CASE/stopped"
elif [[ $1 == inspect ]]; then
    case $3 in
        '{{.Image}}') printf 'sha256:previous-image\n' ;;
        '{{range $name, $network := .NetworkSettings.Networks}}{{println $name}}{{end}}') printf 'npm_default\n' ;;
        '{{.State.Health.Status}}')
            if [[ $MOCK_SCENARIO == health-failure ]]; then printf 'unhealthy\n'; else printf 'healthy\n'; fi ;;
        '{{.State.Running}}') printf 'false\n' ;;
        *) exit 2 ;;
    esac
elif [[ $1 == run ]]; then
    [[ -f $MOCK_CASE/stopped ]] || { printf 'Backup happened before stop!\n' >&2; exit 2; }
    [[ $MOCK_SCENARIO != backup-failure ]] || exit 7
    for arg in "$@"; do
        if [[ $arg == type=bind,src=*,dst=/backup ]]; then
            dest=${arg#type=bind,src=}; dest=${dest%,dst=/backup}
            tar -czf "$dest/data.tar.gz" -C "$MOCK_DATA" .
        fi
    done
fi
MOCK
chmod +x "$fixtures/bin/"*
export PATH="$fixtures/bin:$PATH" MOCK_DATA="$fixtures/data"

run_case() {
    local name=$1 expected=$2 domain=$3 mx=$4 mode=${5:-} network=${6:-} configure_proxy=${7:-false} configure_mx=${8:-false} new_http_port=${9:-}
    export MOCK_CASE="$fixtures/$name" MOCK_SCENARIO=$name
    export MOCK_OLD_CADDY=false
    if [[ $name == first-https || $name == npm-first-auto || $name == first-custom-port ]]; then export MOCK_SCENARIO=first; fi
    case $name in
        proxy-build-failure) export MOCK_SCENARIO=build-failure ;;
        proxy-backup-failure) export MOCK_SCENARIO=backup-failure ;;
        proxy-health-failure) export MOCK_SCENARIO=health-failure ;;
        caddy-to-direct) export MOCK_OLD_CADDY=true ;;
    esac
    mkdir -p "$MOCK_CASE"
    cp "$scripts/server-deploy.sh" "$MOCK_CASE/helper.sh"
    cp "$fixtures/package.tar.gz" "$MOCK_CASE/source.tar.gz"
    if [[ $MOCK_SCENARIO != first ]]; then
        mkdir -p "$MOCK_CASE/app"
        printf 'ADMIN_PASSWORD=existing-password-123\nAPP_ORIGIN=https://mail.example.com\nMAIL_DOMAIN="mail.example.com"\nSMTP_ENABLED=\x27true\x27\nSMTP_HOSTNAME=mx.example.com\n' > "$MOCK_CASE/app/.env"
        if [[ $name == npm-update || $name == npm-missing || $name == npm-to-direct || $name == proxy-*-failure || $name == mx-only ]]; then
            printf 'PROXY_MODE=npm\nNPM_NETWORK=%s\nNPM_UPSTREAM=mail-lite-app\n' "${network:-npm_default}" >> "$MOCK_CASE/app/.env"
        fi
        if [[ $configure_proxy == true || $configure_mx == true ]]; then
            printf 'HTTP_PORT=8099\nHTTP_BIND=127.0.0.1\nUNRELATED_SETTING=keep-this\nLITERAL_VALUE=$(touch should-not-exist)\n' >> "$MOCK_CASE/app/.env"
        fi
        if [[ $name == update-custom-port ]]; then printf 'HTTP_PORT=8099\n' >> "$MOCK_CASE/app/.env"; fi
        cp "$MOCK_CASE/app/.env" "$MOCK_CASE/original.env"
    fi
    local result=0
    local deployment_action=update
    if [[ $MOCK_SCENARIO == first ]]; then deployment_action=deploy; fi
    local extra_args=()
    if [[ -n $mode ]]; then extra_args=("$mode" "$network"); fi
    if [[ $configure_proxy == true || $configure_mx == true ]]; then extra_args+=("$configure_proxy" "$configure_mx"); fi
    if [[ -n $new_http_port ]]; then extra_args=("$mode" "$network" "$configure_proxy" "$configure_mx" "$new_http_port" false); fi
    bash "$MOCK_CASE/helper.sh" "$MOCK_CASE/source.tar.gz" "$MOCK_CASE/app" mail-lite 192.0.2.1 "$domain" "$mx" mx.example.com "$deployment_action" "${extra_args[@]}" > "$MOCK_CASE/output" 2>&1 || result=$?
    if [[ $expected == success && $result != 0 || $expected == failure && $result == 0 ]]; then
        cat "$MOCK_CASE/output" >&2
        printf 'Unexpected result for %s: %s\n' "$name" "$result" >&2
        exit 1
    fi
    if [[ $MOCK_SCENARIO != first && ( $configure_proxy != true && $configure_mx != true || $MOCK_SCENARIO == build-failure || $MOCK_SCENARIO == backup-failure ) ]]; then
        cmp "$MOCK_CASE/original.env" "$MOCK_CASE/app/.env"
    fi
}

run_case first success '' false
grep -q '^HTTP_BIND=0.0.0.0$' "$MOCK_CASE/app/.env"
grep -q '^Management URL: http://192.0.2.1:8080/admin/$' "$MOCK_CASE/output"
[[ -d $MOCK_CASE/app/.deploy/current ]]
[[ $(cat "$MOCK_CASE/app/.deploy/project") == mail-lite ]]
printf 'PASS: first deployment creates HTTP configuration and waits for readiness\n'

run_case first-https success mail.example.com true
grep -q '^HTTP_BIND=127.0.0.1$' "$MOCK_CASE/app/.env"
grep -q '^APP_ORIGIN=https://mail.example.com$' "$MOCK_CASE/app/.env"
grep -q '/compose.mx.yml|' "$MOCK_CASE/commands"
grep -q '|up|-d|--no-build|caddy|' "$MOCK_CASE/commands"
printf 'PASS: first deployment can enable automatic HTTPS and MX together\n'

run_case npm-first-auto success mail.example.com true npm ''
grep -q '^NPM_NETWORK=npm_default$' "$MOCK_CASE/app/.env"
grep -q '^HTTP_BIND=127.0.0.1$' "$MOCK_CASE/app/.env"
grep -q '/compose.npm.yml|' "$MOCK_CASE/commands"
! grep -q '/compose.https.yml|' "$MOCK_CASE/commands"
! grep -q '|caddy|' "$MOCK_CASE/commands"
grep -q 'hostname=mail-lite-app, port=8080' "$MOCK_CASE/output"
grep -q '^Configured domain URL (requires DNS/proxy/HTTPS setup): https://mail.example.com/admin/$' "$MOCK_CASE/output"
! grep -q '^Application ready: https://' "$MOCK_CASE/output"
printf 'PASS: NPM deployment detects its shared network and leaves HTTPS to the existing proxy\n'

run_case npm-update success changed.example.com false caddy ''
grep -q '/compose.npm.yml|' "$MOCK_CASE/commands"
grep -q '/compose.mx.yml|' "$MOCK_CASE/commands"
! grep -q '/compose.https.yml|' "$MOCK_CASE/commands"
! grep -q '|caddy|' "$MOCK_CASE/commands"
printf 'PASS: NPM updates preserve the server proxy mode, network and MX settings\n'

run_case npm-missing failure mail.example.com false npm missing-network
! grep -q '^stop|' "$MOCK_CASE/commands"
! grep -q '|build|' "$MOCK_CASE/commands"
printf 'PASS: missing NPM network is reported before building or stopping the application\n'

run_case update success changed.example.com false
grep -q '/compose.mx.yml|' "$MOCK_CASE/commands"
grep -q '/compose.https.yml|' "$MOCK_CASE/commands"
snapshot=$(find "$MOCK_CASE/app/.deploy/backups" -name data.tar.gz)
for file in mail-lite.db mx-mail.db data.key mail-lite.db-wal; do
    tar -xOzf "$snapshot" "./$file" | cmp - "$fixtures/data/$file"
done
stop_line=$(grep -n '^stop|' "$MOCK_CASE/commands" | cut -d: -f1)
backup_line=$(grep -n '^run|' "$MOCK_CASE/commands" | cut -d: -f1)
up_line=$(grep -n '|up|-d|--no-build|mail|' "$MOCK_CASE/commands" | cut -d: -f1)
[[ $stop_line -lt $backup_line && $backup_line -lt $up_line ]]
printf 'PASS: update preserves .env, HTTPS and MX; backs up databases, WAL and key before replacement\n'

run_case build-failure failure '' false
! grep -q '^stop|' "$MOCK_CASE/commands"
! grep -q '|up|' "$MOCK_CASE/commands"
printf 'PASS: failed build leaves the existing service running\n'

run_case backup-failure failure '' false
grep -q '^start|old-container|' "$MOCK_CASE/commands"
! grep -q '|up|' "$MOCK_CASE/commands"
printf 'PASS: failed backup restarts the previous container and cancels replacement\n'

run_case health-failure failure '' false
[[ ! -e $MOCK_CASE/app/.deploy/current ]]
[[ $(cat "$MOCK_CASE/app/.deploy/project") == mail-lite ]]
grep -q 'Readiness check failed' "$MOCK_CASE/output"
[[ -n $(find "$MOCK_CASE/app/.deploy/backups" -name data.tar.gz) ]]
printf 'PASS: unhealthy replacement returns failure and retains the data snapshot\n'

cp "$fixtures/package.tar.gz" "$MOCK_CASE/retry.tar.gz"
result=0
bash "$scripts/server-deploy.sh" "$MOCK_CASE/retry.tar.gz" "$MOCK_CASE/app" different-project 192.0.2.1 '' false '' update > "$MOCK_CASE/conflict-output" 2>&1 || result=$?
[[ $result != 0 ]]
grep -q 'Compose project differs' "$MOCK_CASE/conflict-output"
[[ $(cat "$MOCK_CASE/app/.deploy/project") == mail-lite ]]
printf 'PASS: failed deployment retains its project and rejects a different data-volume identity on retry\n'

run_case npm-to-direct success '' false direct '' true false
grep -q '^PROXY_MODE=direct$' "$MOCK_CASE/app/.env"
grep -q '^APP_ORIGIN=http://192.0.2.1:8099$' "$MOCK_CASE/app/.env"
grep -q '^HTTP_BIND=0.0.0.0$' "$MOCK_CASE/app/.env"
grep -q '^MAIL_DOMAIN=$' "$MOCK_CASE/app/.env"
grep -q '^HTTP_PORT=8099$' "$MOCK_CASE/app/.env"
grep -q '^ADMIN_PASSWORD=existing-password-123$' "$MOCK_CASE/app/.env"
grep -q "^SMTP_ENABLED='true'$" "$MOCK_CASE/app/.env"
grep -Fq 'LITERAL_VALUE=$(touch should-not-exist)' "$MOCK_CASE/app/.env"
grep -q '^UNRELATED_SETTING=keep-this$' "$MOCK_CASE/app/.env"
grep -q '^Management URL: http://192.0.2.1:8099/admin/$' "$MOCK_CASE/output"
cmp "$MOCK_CASE/original.env" "$(find "$MOCK_CASE/app/.deploy/backups" -name env)"
! grep -q '/compose.npm.yml|' "$MOCK_CASE/commands"
[[ -z $(find "$MOCK_CASE/app/.deploy" -maxdepth 1 -name 'settings.*.env') ]]
printf 'PASS: explicit IP access replaces proxy settings and retains credentials, port, MX and unrelated values\n'

run_case caddy-to-direct success '' false direct '' true false
grep -q '^stop|--time|30|old-caddy|' "$MOCK_CASE/commands"
healthy_line=$(grep -n '{{.State.Health.Status}}' "$MOCK_CASE/commands" | cut -d: -f1)
caddy_stop_line=$(grep -n '^stop|--time|30|old-caddy|' "$MOCK_CASE/commands" | cut -d: -f1)
[[ $healthy_line -lt $caddy_stop_line ]]
grep -q '^ps|-q|--filter|label=com.docker.compose.project=mail-lite|--filter|label=com.docker.compose.service=caddy|' "$MOCK_CASE/commands"
printf 'PASS: switching away from Caddy stops only this project proxy after the application is healthy\n'

run_case configure-domain success new.example.com false caddy '' true false
grep -q '^APP_ORIGIN=https://new.example.com$' "$MOCK_CASE/app/.env"
grep -q '^HTTP_BIND=127.0.0.1$' "$MOCK_CASE/app/.env"
grep -q '^MAIL_DOMAIN=new.example.com$' "$MOCK_CASE/app/.env"
grep -q '^Configured domain URL (requires DNS/proxy/HTTPS setup): https://new.example.com/admin/$' "$MOCK_CASE/output"
! grep -q '^Management URL:' "$MOCK_CASE/output"
printf 'PASS: explicit domain configuration selects HTTPS without claiming public DNS or certificate readiness\n'

run_case mx-only success changed.example.com true direct npm_default false true
grep -q '^PROXY_MODE=npm$' "$MOCK_CASE/app/.env"
grep -q '^APP_ORIGIN=https://mail.example.com$' "$MOCK_CASE/app/.env"
grep -q '^SMTP_ENABLED=true$' "$MOCK_CASE/app/.env"
grep -q '^SMTP_HOSTNAME=mx.example.com$' "$MOCK_CASE/app/.env"
printf 'PASS: explicit MX settings retain the existing proxy configuration\n'

run_case proxy-build-failure failure '' false direct '' true false
! grep -q '^stop|' "$MOCK_CASE/commands"
[[ -z $(find "$MOCK_CASE/app/.deploy" -maxdepth 1 -name 'settings.*.env') ]]
printf 'PASS: failed build retains the original server configuration and removes staged settings\n'

run_case proxy-backup-failure failure '' false direct '' true false
grep -q '^start|old-container|' "$MOCK_CASE/commands"
printf 'PASS: failed backup retains the original configuration and resumes the previous application\n'

run_case proxy-health-failure failure '' false direct '' true false
grep -q '^PROXY_MODE=direct$' "$MOCK_CASE/app/.env"
cmp "$MOCK_CASE/original.env" "$(find "$MOCK_CASE/app/.deploy/backups" -name env)"
grep -q 'previous settings are in the backup' "$MOCK_CASE/output"
printf 'PASS: unhealthy replacement keeps matching new settings with the original configuration backed up\n'

run_case first-custom-port success '' false direct '' false false 8081
grep -q '^HTTP_PORT=8081$' "$MOCK_CASE/app/.env"
grep -q '^APP_ORIGIN=http://192.0.2.1:8081$' "$MOCK_CASE/app/.env"
printf 'PASS: new installations use the requested HTTP port in settings and management URL\n'

run_case update-custom-port success '' false direct '' false false 8081
grep -q '^HTTP_PORT=8099$' "$MOCK_CASE/app/.env"
printf 'PASS: the new-install port argument does not change an existing installation port\n'

run_fresh_case() {
    local name=$1 expected=$2 initial_state=${3:-empty} requested_port=${4:-8081} result=0
    export MOCK_CASE="$fixtures/$name" MOCK_SCENARIO=first MOCK_OLD_CADDY=false
    export MOCK_FRESH_CONTAINERS=false MOCK_FRESH_VOLUME=false MOCK_FRESH_OTHER_VOLUME=false
    mkdir -p "$MOCK_CASE/app"
    cp "$fixtures/package.tar.gz" "$MOCK_CASE/source.tar.gz"
    case $initial_state in
        env) printf 'ADMIN_PASSWORD=do-not-change-existing\n' > "$MOCK_CASE/app/.env"; cp "$MOCK_CASE/app/.env" "$MOCK_CASE/original.env" ;;
        marker) mkdir "$MOCK_CASE/app/.deploy"; printf 'existing-project\n' > "$MOCK_CASE/app/.deploy/project" ;;
        current) mkdir -p "$MOCK_CASE/app/.deploy/current" ;;
        containers) export MOCK_FRESH_CONTAINERS=true ;;
        volume) export MOCK_FRESH_VOLUME=true ;;
        other-volume) export MOCK_FRESH_OTHER_VOLUME=true ;;
    esac
    bash "$scripts/server-deploy.sh" "$MOCK_CASE/source.tar.gz" "$MOCK_CASE/app" fresh-mail 192.0.2.1 '' false '' deploy direct '' false false "$requested_port" true > "$MOCK_CASE/output" 2>&1 || result=$?
    if [[ $expected == success && $result != 0 || $expected == failure && $result == 0 ]]; then
        cat "$MOCK_CASE/output" >&2
        printf 'Unexpected fresh-install result for %s: %s\n' "$name" "$result" >&2
        exit 1
    fi
    if [[ $expected == failure ]]; then
        if [[ -f $MOCK_CASE/commands ]]; then
            ! grep -Eq '^(stop|run)\||\|(build|up)\|' "$MOCK_CASE/commands"
        fi
        if [[ $initial_state == env ]]; then cmp "$MOCK_CASE/original.env" "$MOCK_CASE/app/.env";
        else [[ ! -e $MOCK_CASE/app/.env ]]; fi
    fi
}

run_fresh_case fresh-empty success
grep -q '^HTTP_PORT=8081$' "$MOCK_CASE/app/.env"
grep -q '^APP_ORIGIN=http://192.0.2.1:8081$' "$MOCK_CASE/app/.env"
grep -Eq '^ADMIN_PASSWORD=[a-f0-9]{48}$' "$MOCK_CASE/app/.env"
grep -q '^ps|-aq|--filter|label=com.docker.compose.project=fresh-mail|' "$MOCK_CASE/commands"
! grep -Eq '^(stop|run)\|' "$MOCK_CASE/commands"
printf 'PASS: fresh deployment starts with an unused project, a new administrator password and the requested port\n'

run_fresh_case fresh-existing-env failure env
[[ ! -e $MOCK_CASE/commands ]]
run_fresh_case fresh-existing-marker failure marker
[[ ! -e $MOCK_CASE/commands ]]
[[ $(cat "$MOCK_CASE/app/.deploy/project") == existing-project ]]
run_fresh_case fresh-existing-current failure current
[[ ! -e $MOCK_CASE/commands ]]
printf 'PASS: fresh deployment rejects existing installation settings and markers before Docker operations\n'

run_fresh_case fresh-existing-container failure containers
grep -q 'including stopped containers' "$MOCK_CASE/output"
[[ ! -e $MOCK_CASE/app/.deploy/project ]]
run_fresh_case fresh-existing-volume failure volume
grep -q 'existing data volume' "$MOCK_CASE/output"
[[ ! -e $MOCK_CASE/app/.deploy/project ]]
printf 'PASS: fresh deployment refuses stopped project containers and existing data volumes without reusing or stopping them\n'

run_fresh_case fresh-existing-other-volume failure other-volume
grep -q 'existing volumes' "$MOCK_CASE/output"
grep -q '^volume|ls|-q|--filter|label=com.docker.compose.project=fresh-mail|' "$MOCK_CASE/commands"
[[ ! -e $MOCK_CASE/app/.deploy/project ]]
printf 'PASS: fresh deployment refuses other existing project volumes, including Caddy certificate data\n'

run_fresh_case fresh-invalid-port-zero failure empty 0
run_fresh_case fresh-invalid-port-high failure empty 65536
printf 'PASS: invalid new-install ports fail before configuration or Docker operations\n'
printf 'Test fixtures: %s\n' "$fixtures"
