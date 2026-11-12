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
[[ ! -e $MOCK_CASE/curl-arguments ]]
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
printf 'PASS: repository-root source, automatic HTTPS and MX arguments reach the deployment engine\n'

run_case npm 0 --yes --source "$source_dir" --dir "$fixtures/npm-app" --mode npm \
    --domain mail.example.com --npm-network npm_default
assert_arg 8 npm
assert_arg 9 npm_default
printf 'PASS: existing NPM network is passed without losing optional argument positions\n'

mkdir -p "$fixtures/existing-app/.deploy"
printf 'ADMIN_PASSWORD=existing-password\nPROXY_MODE=caddy\n' > "$fixtures/existing-app/.env"
printf 'custom-mail\n' > "$fixtures/existing-app/.deploy/project"
cp "$fixtures/existing-app/.env" "$fixtures/original.env"
run_case update 0 --yes --source "$source_dir" --dir "$fixtures/existing-app"
assert_arg 2 custom-mail
assert_arg 7 update
cmp "$fixtures/original.env" "$fixtures/existing-app/.env"
printf 'PASS: repeated installation reuses the original Compose project and server configuration\n'

run_case project-conflict 1 --yes --source "$source_dir" --dir "$fixtures/existing-app" --project other
assert_not_deployed
grep -q 'Existing installation uses project custom-mail' "$MOCK_CASE/output"
run_case invalid-source 1 --yes --source "$fixtures/typo" --dir "$fixtures/invalid-app" --host 192.0.2.10
assert_not_deployed
run_case missing-host 1 --yes --source "$source_dir" --dir "$fixtures/missing-host-app"
assert_not_deployed
run_case missing-domain 1 --yes --source "$source_dir" --dir "$fixtures/missing-domain-app" --mode caddy
assert_not_deployed
run_case root-path 1 --yes --source "$source_dir" --dir /./ --host 192.0.2.10
assert_not_deployed
run_case no-terminal 1 --source "$source_dir" --dir "$fixtures/no-terminal-app" --host 192.0.2.10
assert_not_deployed
grep -q 'No interactive terminal' "$MOCK_CASE/output"
mkdir -p "$fixtures/incomplete-source"
printf 'fixture\n' > "$fixtures/incomplete-source/Dockerfile"
run_case incomplete-source 1 --yes --source "$fixtures/incomplete-source" --dir "$fixtures/incomplete-app" --host 192.0.2.10
assert_not_deployed
grep -q 'Missing compose.yml' "$MOCK_CASE/output"
printf 'PASS: invalid projects/sources/hosts/domains/root paths and noninteractive prompts fail before deployment\n'

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
