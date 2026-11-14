#!/bin/sh
# Export a built application image for nodes without registry/npm access.
# Usage: sh export-node-image.sh cloud-mail-app:release /absolute/new-directory
set -eu
umask 077

fail() { printf '%s\n' "$*" >&2; exit 1; }
[ "$#" -eq 2 ] || fail 'Usage: sh docker/export-node-image.sh <image> <absolute-new-directory>'
node_export_image=$1
node_export_output=$2
case "$node_export_image" in ''|-*) fail 'Specify an existing Docker image tag or ID.' ;; esac
case "$node_export_output" in /*) ;; *) fail 'Output directory must be an absolute path.' ;; esac
while [ "${node_export_output%/}" != "$node_export_output" ]; do node_export_output=${node_export_output%/}; done
node_export_name=$(basename "$node_export_output")
case "$node_export_name" in ''|.|..) fail 'Choose a new output directory, not a parent or filesystem root.' ;; esac
node_export_parent=$(CDPATH= cd "$(dirname "$node_export_output")" && pwd -P) || fail 'The output parent directory must already exist.'
node_export_output="$node_export_parent/$node_export_name"
for node_export_tool in docker gzip sha256sum mktemp mkfifo; do
  command -v "$node_export_tool" >/dev/null 2>&1 || fail "Missing required tool: $node_export_tool"
done
[ ! -e "$node_export_output" ] && [ ! -L "$node_export_output" ] || fail 'Output directory already exists; choose a new directory for each release.'

node_export_image_id=$(docker image inspect --format '{{.Id}}' "$node_export_image")
case "$node_export_image_id" in sha256:*) node_export_image_digest=${node_export_image_id#sha256:} ;; *) fail 'Docker returned an invalid image ID.' ;; esac
[ "${#node_export_image_digest}" -eq 64 ] || fail 'Docker returned an invalid image ID.'
case "$node_export_image_digest" in *[!0-9a-f]*) fail 'Docker returned an invalid image ID.' ;; esac

# mkdir is exclusive, so publication never overwrites another release.
mkdir -m 700 "$node_export_output"
node_export_tmp=''
node_export_pid=''
node_export_complete=false
cleanup() {
  node_export_status=$?
  trap - 0
  if [ -n "$node_export_pid" ]; then
    kill "$node_export_pid" 2>/dev/null || true
    wait "$node_export_pid" 2>/dev/null || true
  fi
  if [ -n "$node_export_tmp" ]; then
    rm -f "$node_export_tmp/image.pipe" "$node_export_tmp/node-image.tar.gz" "$node_export_tmp/node-image.json"
    rmdir "$node_export_tmp" 2>/dev/null || true
  fi
  if [ "$node_export_complete" = false ]; then
    rm -f "$node_export_output/node-image.tar.gz" "$node_export_output/node-image.json"
    rmdir "$node_export_output" 2>/dev/null || true
  fi
  exit "$node_export_status"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' HUP TERM

node_export_tmp=$(mktemp -d "$node_export_output/.export-XXXXXX")
mkfifo "$node_export_tmp/image.pipe"
# Check both processes explicitly: POSIX sh pipelines do not have pipefail.
# The FIFO avoids storing a second, uncompressed copy on small server disks.
docker image save "$node_export_image_id" > "$node_export_tmp/image.pipe" &
node_export_pid=$!
if ! gzip -c < "$node_export_tmp/image.pipe" > "$node_export_tmp/node-image.tar.gz"; then
  fail 'Image compression failed; no release was published.'
fi
if wait "$node_export_pid"; then
  node_export_pid=''
else
  node_export_pid=''
  fail 'Docker image export failed; no release was published.'
fi
[ -s "$node_export_tmp/node-image.tar.gz" ] || fail 'Docker image export produced an empty archive.'
node_export_checksum=$(sha256sum "$node_export_tmp/node-image.tar.gz")
node_export_checksum=${node_export_checksum%% *}
[ "${#node_export_checksum}" -eq 64 ] || fail 'Cannot calculate the image archive checksum.'
case "$node_export_checksum" in *[!0-9a-f]*) fail 'Cannot calculate the image archive checksum.' ;; esac
printf '{"imageId":"%s","sha256":"%s","format":"docker-image"}\n' \
  "$node_export_image_id" "$node_export_checksum" > "$node_export_tmp/node-image.json"
chmod 600 "$node_export_tmp/node-image.tar.gz" "$node_export_tmp/node-image.json"
mv "$node_export_tmp/node-image.tar.gz" "$node_export_output/node-image.tar.gz"
mv "$node_export_tmp/node-image.json" "$node_export_output/node-image.json"
node_export_complete=true
printf 'Node image release exported to %s\n' "$node_export_output"
