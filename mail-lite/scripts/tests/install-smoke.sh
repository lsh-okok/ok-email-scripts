#!/usr/bin/env bash
# Exercise the Linux entry point with real source archives and a stub deployer.
# No network, Docker daemon, package manager, or server installation is used.
set -Eeuo pipefail
repo_root=$(cd -- "$(dirname -- "$0")/../../.." && pwd -P)
fixtures=$(mktemp -d /tmp/mail-lite-install-test.XXXXXX)
mkdir -p "$fixtures/bin" "$fixtures/standalone" "$fixtures/cases"
cp "$repo_root/install-mail-lite.sh" "$fixtures/standalone/install-mail-lite.sh"
source_dir="$fixtures/source with spaces/mail-lite"
mkdir -p "$source_dir/scripts" "$source_dir/internal/core" "$source_dir/web/ui/src" \
    "$source_dir/web/dist" "$source_dir/data" "$source_dir/bin" \
    "$source_dir/web/ui/node_modules" "$source_dir/web/ui/dist" \
    "$source_dir/internal/.cache" "$fixtures/outside"

for file in Dockerfile compose.yml compose.https.yml compose.npm.yml compose.mx.yml go.mod go.sum .env.example .dockerignore; do
    printf 'build fixture\n' > "$source_dir/$file"
done
printf 'uncommitted source\n' > "$source_dir/internal/core/new file.go"
printf 'embedded frontend\n' > "$source_dir/web/dist/index.html"
printf 'frontend source\n' > "$source_dir/web/ui/src/new component.vue"
printf 'build input\n' > "$source_dir/requirements-dev.txt"
printf 'build input\n' > "$source_dir/CMakeLists.txt"
for file in .env .env.production credentials.json accounts.txt STOCK.CSV \
    data/mail-lite.db data/data.key bin/mail-lite \
    web/ui/.env.production web/ui/src/accounts.txt web/ui/src/private.pem \
    web/ui/node_modules/private.js web/ui/dist/private.js internal/.cache/private.go; do
    printf 'PRIVATE_FIXTURE_DO_NOT_PACKAGE\n' > "$source_dir/$file"
done
printf 'PRIVATE_FIXTURE_DO_NOT_PACKAGE\n' > "$fixtures/outside/private.go"
symlink_supported=false
if MSYS=winsymlinks:nativestrict ln -s "$fixtures/outside" "$source_dir/web/ui/src/linked" 2>/dev/null; then
    if [[ -L $source_dir/web/ui/src/linked ]]; then symlink_supported=true; fi
fi

cat > "$source_dir/scripts/server-deploy.sh" <<'HELPER'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$@" > "$MOCK_CASE/arguments"
cp "$1" "$MOCK_CASE/package.tar.gz"
touch "$MOCK_CASE/helper-called"
exit "${MOCK_HELPER_EXIT:-0}"
HELPER

cat > "$fixtures/bin/uname" <<'MOCK'
#!/usr/bin/env bash
printf 'Linux\n'
MOCK
cat > "$fixtures/bin/id" <<'MOCK'
#!/usr/bin/env bash
printf '0\n'
MOCK
cat > "$fixtures/bin/flock" <<'MOCK'
#!/usr/bin/env bash
exit 0
MOCK
cat > "$fixtures/bin/mktemp" <<'MOCK'
#!/usr/bin/env bash
set -eu
created=$(/usr/bin/mktemp "$@")
printf '%s\n' "$created" >> "$MOCK_CASE/tempdirs"
printf '%s\n' "$created"
MOCK
cat > "$fixtures/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -eu
endpoint=
for argument in "$@"; do
    case $argument in
        https://api.ipify.org|https://checkip.amazonaws.com) endpoint=$argument ;;
    esac
done
if [[ -n $endpoint ]]; then
    printf '%s\n' "$@" >> "$MOCK_CASE/ip-arguments"
    if [[ $endpoint == https://api.ipify.org ]]; then
        printf '%s' "${MOCK_IPIFY_RESPONSE-93.184.216.34}"
        exit "${MOCK_IPIFY_EXIT:-0}"
    fi
    printf '%s' "${MOCK_CHECKIP_RESPONSE-93.184.216.35}"
    exit "${MOCK_CHECKIP_EXIT:-0}"
fi
printf '%s\n' "$@" > "$MOCK_CASE/curl-arguments"
[[ ${MOCK_DOWNLOAD_FAIL:-false} != true ]] || exit 22
destination=
while (($#)); do
    if [[ $1 == -o ]]; then destination=$2; shift 2; else shift; fi
done
[[ -n $destination && -n ${MOCK_DOWNLOAD_ARCHIVE:-} ]] || exit 23
cp "$MOCK_DOWNLOAD_ARCHIVE" "$destination"
MOCK
for program in docker apt-get sudo systemctl; do
    cat > "$fixtures/bin/$program" <<'MOCK'
#!/usr/bin/env bash
printf 'Unexpected external installation command: %s\n' "$0" >&2
exit 90
MOCK
done
chmod +x "$fixtures/bin/"*
export PATH="$fixtures/bin:$PATH"
unset SSH_CONNECTION

run_case() {
    local name=$1 expected=$2 result=0
    shift 2
    export MOCK_CASE="$fixtures/cases/$name"
    mkdir -p "$MOCK_CASE"
    bash "$fixtures/standalone/install-mail-lite.sh" "$@" </dev/null > "$MOCK_CASE/output" 2>&1 || result=$?
    if [[ $result != "$expected" ]]; then
        cat "$MOCK_CASE/output" >&2
        printf 'Unexpected result for %s: expected %s, got %s\n' "$name" "$expected" "$result" >&2
        exit 1
    fi
    if [[ -f $MOCK_CASE/tempdirs ]]; then
        while IFS= read -r temporary_dir; do
            [[ ! -e $temporary_dir ]] || { printf 'Temporary directory was not cleaned: %s\n' "$temporary_dir" >&2; exit 1; }
        done < "$MOCK_CASE/tempdirs"
    fi
}
assert_arg() {
    local index=$1 expected=$2
    mapfile -t actual_args < "$MOCK_CASE/arguments"
    [[ ${actual_args[$index]} == "$expected" ]] || {
        printf 'Argument %s: expected <%s>, got <%s>\n' "$index" "$expected" "${actual_args[$index]}" >&2
        exit 1
    }
}
assert_not_deployed() { [[ ! -e $MOCK_CASE/helper-called && ! -e $MOCK_CASE/curl-arguments ]]; }

run_case local-direct 0 --yes --source "$source_dir" --dir "$fixtures/direct-app" --host 192.0.2.10
assert_arg 1 "$fixtures/direct-app"
assert_arg 2 mail-lite
assert_arg 3 192.0.2.10
assert_arg 4 ''
assert_arg 5 false
assert_arg 6 ''
assert_arg 7 deploy
assert_arg 8 direct
assert_arg 9 ''
assert_arg 10 false
assert_arg 11 false
[[ ! -e $MOCK_CASE/curl-arguments ]]
[[ ! -e $MOCK_CASE/ip-arguments ]]
tar -tzf "$MOCK_CASE/package.tar.gz" > "$MOCK_CASE/entries"
for file in Dockerfile .env.example .dockerignore internal/core/new\ file.go \
    web/dist/index.html web/ui/src/new\ component.vue requirements-dev.txt CMakeLists.txt; do
    grep -Fxq "./$file" "$MOCK_CASE/entries"
done
tar -xOzf "$MOCK_CASE/package.tar.gz" > "$MOCK_CASE/package-content"
! grep -q PRIVATE_FIXTURE_DO_NOT_PACKAGE "$MOCK_CASE/package-content"
tar -xOzf "$MOCK_CASE/package.tar.gz" './internal/core/new file.go' | grep -Fxq 'uncommitted source'
printf 'PASS: local source with spaces includes current build inputs and excludes runtime/private files\n'
if [[ $symlink_supported == true ]]; then
    ! grep -q linked "$MOCK_CASE/entries"
    printf 'PASS: source symlinks are not followed into private directories\n'
else
    printf 'SKIP: native symlink fixture is unavailable on this host\n'
fi

run_case caddy-mx 0 --yes --source "$fixtures/source with spaces" --dir "$fixtures/caddy-app" \
    --mode caddy --domain mail.example.com --mx-host mx.example.com
assert_arg 3 mail.example.com
assert_arg 4 mail.example.com
assert_arg 5 true
assert_arg 6 mx.example.com
assert_arg 8 caddy
assert_arg 10 true
assert_arg 11 true
printf 'PASS: repository-root source, automatic HTTPS and MX arguments reach the deployment engine\n'

run_case npm 0 --yes --source "$source_dir" --dir "$fixtures/npm-app" --mode npm \
    --domain mail.example.com --npm-network npm_default
assert_arg 8 npm
assert_arg 9 npm_default
assert_arg 10 true
assert_arg 11 false
printf 'PASS: existing NPM network is passed without losing optional argument positions\n'

mkdir -p "$fixtures/existing-app/.deploy"
printf 'ADMIN_PASSWORD=existing-password\nPROXY_MODE=caddy\n' > "$fixtures/existing-app/.env"
printf 'custom-mail\n' > "$fixtures/existing-app/.deploy/project"
cp "$fixtures/existing-app/.env" "$fixtures/original.env"
run_case update 0 --yes --source "$source_dir" --dir "$fixtures/existing-app"
assert_arg 2 custom-mail
assert_arg 7 update
assert_arg 10 false
assert_arg 11 false
[[ ! -e $MOCK_CASE/ip-arguments ]]
cmp "$fixtures/original.env" "$fixtures/existing-app/.env"
printf 'PASS: repeated installation reuses the original Compose project and server configuration\n'

run_case auto-host 0 --yes --source "$source_dir" --dir "$fixtures/auto-host-app"
assert_arg 3 93.184.216.34
assert_arg 5 false
assert_arg 8 direct
assert_arg 10 false
assert_arg 11 false
grep -q 'Detected public IPv4: 93.184.216.34' "$MOCK_CASE/output"
! grep -q 'https://checkip.amazonaws.com' "$MOCK_CASE/ip-arguments"
printf 'PASS: default installation detects public IPv4 and starts direct HTTP without domain/MX options\n'

run_case no-terminal 0 --source "$source_dir" --dir "$fixtures/no-terminal-app"
assert_arg 3 93.184.216.34
printf 'PASS: default installation works without a terminal or --yes when IP detection succeeds\n'

export MOCK_IPIFY_RESPONSE=192.0.3.1
run_case public-192-range 0 --yes --source "$source_dir" --dir "$fixtures/public-192-app"
assert_arg 3 192.0.3.1
unset MOCK_IPIFY_RESPONSE
printf 'PASS: routable addresses adjacent to reserved ranges remain eligible for detection\n'

export SSH_CONNECTION='198.51.100.2 40000 93.184.216.36 22'
run_case ssh-public 0 --yes --source "$source_dir" --dir "$fixtures/ssh-public-app"
assert_arg 3 93.184.216.36
[[ ! -e $MOCK_CASE/ip-arguments ]]
export SSH_CONNECTION='198.51.100.2 40000 172.31.20.58 22' MOCK_IPIFY_EXIT=28
run_case aws-private-fallback 0 --yes --source "$source_dir" --dir "$fixtures/aws-private-app"
assert_arg 3 93.184.216.35
grep -Fxq 'https://api.ipify.org' "$MOCK_CASE/ip-arguments"
grep -Fxq 'https://checkip.amazonaws.com' "$MOCK_CASE/ip-arguments"
unset SSH_CONNECTION MOCK_IPIFY_EXIT
printf 'PASS: public SSH addresses are reused; AWS private addresses and failed lookups fall back to the next IP service\n'

export MOCK_IPIFY_RESPONSE='<html>93.184.216.34</html>'
run_case malformed-first-service 0 --yes --source "$source_dir" --dir "$fixtures/malformed-first-app"
assert_arg 3 93.184.216.35
export MOCK_IPIFY_RESPONSE=$' \r\n93.184.216.34\r\n '
run_case whitespace-response 0 --yes --source "$source_dir" --dir "$fixtures/whitespace-app"
assert_arg 3 93.184.216.34
unset MOCK_IPIFY_RESPONSE
printf 'PASS: malformed IP-service responses are rejected and surrounding whitespace is accepted\n'

run_case switch-to-ip 0 --source "$source_dir" --dir "$fixtures/existing-app" --mode direct
assert_arg 2 custom-mail
assert_arg 3 93.184.216.34
assert_arg 7 update
assert_arg 8 direct
assert_arg 10 true
assert_arg 11 false
run_case configure-domain-later 0 --yes --source "$source_dir" --dir "$fixtures/existing-app" --domain later.example.com
assert_arg 3 later.example.com
assert_arg 4 later.example.com
assert_arg 8 caddy
assert_arg 10 true
[[ ! -e $MOCK_CASE/ip-arguments ]]
run_case configure-mx-only 0 --yes --source "$source_dir" --dir "$fixtures/existing-app" --mx-host mx.example.com
assert_arg 5 true
assert_arg 6 mx.example.com
assert_arg 10 false
assert_arg 11 true
[[ ! -e $MOCK_CASE/ip-arguments ]]
printf 'PASS: existing installations can explicitly switch to IP, configure a domain later, or enable MX without switching the proxy\n'

invalid_response_number=0
for invalid_response in 0.1.2.3 10.2.3.4 100.64.1.2 100.127.1.2 127.0.0.1 169.254.1.2 \
    172.16.0.1 172.31.20.58 192.168.1.1 192.0.2.1 198.18.0.1 198.51.100.1 203.0.113.1 \
    224.0.0.1 255.255.255.255 256.2.3.4 93.184.216.034 '<html>93.184.216.34</html>' $'93.184.\n216.34' ''; do
    invalid_response_number=$((invalid_response_number + 1))
    export MOCK_IPIFY_RESPONSE="$invalid_response" MOCK_CHECKIP_RESPONSE="$invalid_response"
    run_case "invalid-ip-$invalid_response_number" 1 --yes --source "$source_dir" --dir "$fixtures/invalid-ip-app-$invalid_response_number"
    assert_not_deployed
    grep -q 'Could not detect a public IPv4.*--host' "$MOCK_CASE/output"
done
unset MOCK_IPIFY_RESPONSE MOCK_CHECKIP_RESPONSE
export MOCK_IPIFY_EXIT=28 MOCK_CHECKIP_EXIT=28
run_case ip-services-failed 1 --yes --source "$source_dir" --dir "$fixtures/ip-failure-app"
assert_not_deployed
grep -q 'Could not detect a public IPv4.*--host' "$MOCK_CASE/output"
[[ ! -e $fixtures/ip-failure-app/.env ]]
unset MOCK_IPIFY_EXIT MOCK_CHECKIP_EXIT
printf 'PASS: private/reserved/malformed IPv4 responses and failed services stop before deployment with a manual override hint\n'

run_case project-conflict 1 --yes --source "$source_dir" --dir "$fixtures/existing-app" --project other
assert_not_deployed
grep -q 'Existing installation uses project custom-mail' "$MOCK_CASE/output"
run_case invalid-source 1 --yes --source "$fixtures/typo" --dir "$fixtures/invalid-app" --host 192.0.2.10
assert_not_deployed
run_case missing-domain 1 --yes --source "$source_dir" --dir "$fixtures/missing-domain-app" --mode caddy
assert_not_deployed
run_case root-path 1 --yes --source "$source_dir" --dir /./ --host 192.0.2.10
assert_not_deployed
mkdir -p "$fixtures/incomplete-source"
printf 'fixture\n' > "$fixtures/incomplete-source/Dockerfile"
run_case incomplete-source 1 --yes --source "$fixtures/incomplete-source" --dir "$fixtures/incomplete-app" --host 192.0.2.10
assert_not_deployed
grep -q 'Missing compose.yml' "$MOCK_CASE/output"
printf 'PASS: invalid projects/sources/domains/root paths fail before deployment\n'

export MOCK_HELPER_EXIT=17
run_case helper-failure 17 --yes --source "$source_dir" --dir "$fixtures/failure-app" --host 192.0.2.10
[[ -e $MOCK_CASE/helper-called ]]
unset MOCK_HELPER_EXIT
printf 'PASS: deployment-engine failure propagates to the installer exit status\n'

mkdir -p "$fixtures/download/repository/mail-lite"
cp -R "$source_dir/." "$fixtures/download/repository/mail-lite/"
tar -czf "$fixtures/github.tar.gz" -C "$fixtures/download" repository
export MOCK_DOWNLOAD_ARCHIVE="$fixtures/github.tar.gz"
run_case download 0 --yes --dir "$fixtures/download-app" --host 192.0.2.10 \
    --repo example/project --ref feature/linux-install
grep -Fxq 'https://codeload.github.com/example/project/tar.gz/feature/linux-install' "$MOCK_CASE/curl-arguments"
[[ -e $MOCK_CASE/helper-called ]]
printf 'PASS: standalone installer downloads the selected repository/ref and deploys its source\n'

export MOCK_DOWNLOAD_FAIL=true
run_case download-failure 22 --yes --dir "$fixtures/download-failure-app" --host 192.0.2.10
[[ ! -e $MOCK_CASE/helper-called ]]
unset MOCK_DOWNLOAD_FAIL
printf 'PASS: failed downloads abort before calling the deployment engine\n'
printf 'PASS: private temporary source directories are cleaned after success and failure\n'
printf 'Test fixtures: %s\n' "$fixtures"
