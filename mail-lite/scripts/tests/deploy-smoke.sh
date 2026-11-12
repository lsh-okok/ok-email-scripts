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
    elif [[ $MOCK_SCENARIO != first ]]; then printf 'old-container\n'; fi
elif [[ $1 == network ]]; then
    [[ ${5:-} != missing-network ]] || exit 1
    printf 'bridge\n'
elif [[ $1 == volume ]]; then
    [[ $MOCK_SCENARIO != first ]]
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
    local name=$1 expected=$2 domain=$3 mx=$4 mode=${5:-} network=${6:-}
    export MOCK_CASE="$fixtures/$name" MOCK_SCENARIO=$name
    if [[ $name == first-https || $name == npm-first-auto ]]; then export MOCK_SCENARIO=first; fi
    mkdir -p "$MOCK_CASE"
    cp "$scripts/server-deploy.sh" "$MOCK_CASE/helper.sh"
    cp "$fixtures/package.tar.gz" "$MOCK_CASE/source.tar.gz"
    if [[ $MOCK_SCENARIO != first ]]; then
        mkdir -p "$MOCK_CASE/app"
        printf 'ADMIN_PASSWORD=existing-password-123\nAPP_ORIGIN=https://mail.example.com\nMAIL_DOMAIN="mail.example.com"\nSMTP_ENABLED=\x27true\x27\nSMTP_HOSTNAME=mx.example.com\n' > "$MOCK_CASE/app/.env"
        if [[ $name == npm-update || $name == npm-missing ]]; then
            printf 'PROXY_MODE=npm\nNPM_NETWORK=%s\nNPM_UPSTREAM=mail-lite-app\n' "${network:-npm_default}" >> "$MOCK_CASE/app/.env"
        fi
        cp "$MOCK_CASE/app/.env" "$MOCK_CASE/original.env"
    fi
    local result=0
    local deployment_action=update
    if [[ $MOCK_SCENARIO == first ]]; then deployment_action=deploy; fi
    local extra_args=()
    if [[ -n $mode ]]; then extra_args=("$mode" "$network"); fi
    bash "$MOCK_CASE/helper.sh" "$MOCK_CASE/source.tar.gz" "$MOCK_CASE/app" mail-lite 192.0.2.1 "$domain" "$mx" mx.example.com "$deployment_action" "${extra_args[@]}" > "$MOCK_CASE/output" 2>&1 || result=$?
    if [[ $expected == success && $result != 0 || $expected == failure && $result == 0 ]]; then
        cat "$MOCK_CASE/output" >&2
        printf 'Unexpected result for %s: %s\n' "$name" "$result" >&2
        exit 1
    fi
    if [[ $MOCK_SCENARIO != first ]]; then cmp "$MOCK_CASE/original.env" "$MOCK_CASE/app/.env"; fi
}

run_case first success '' false
grep -q '^HTTP_BIND=0.0.0.0$' "$MOCK_CASE/app/.env"
grep -q '^Application ready: http://192.0.2.1:8080/admin/$' "$MOCK_CASE/output"
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
printf 'Test fixtures: %s\n' "$fixtures"
