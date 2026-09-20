#!/usr/bin/env bash
# Consistent primary database backup. Run as root or through sudo.
set -Eeuo pipefail
umask 077
APP_DIR="${APP_DIR:-/opt/outlook-email}"
target="$APP_DIR/backups/$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 700 "$target"
python3 - "$APP_DIR/data/outlook_accounts.db" "$target/outlook_accounts.db" <<'PY'
import sqlite3
import sys
source_path, target_path = sys.argv[1:]
with sqlite3.connect(f'file:{source_path}?mode=ro', uri=True) as source:
    with sqlite3.connect(target_path) as target:
        source.backup(target)
        assert target.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
PY
if [[ -f "$APP_DIR/data/cluster/identity.db" ]]; then
  install -d -m 700 "$target/cluster"
  python3 - "$APP_DIR/data/cluster/identity.db" "$target/cluster/identity.db" <<'PY'
import sqlite3
import sys
source_path, target_path = sys.argv[1:]
with sqlite3.connect(f'file:{source_path}?mode=ro', uri=True) as source:
    with sqlite3.connect(target_path) as target:
        source.backup(target)
        assert target.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
PY
fi
cp "$APP_DIR/.env" "$target/.env"
printf '%s\n' "$target"
