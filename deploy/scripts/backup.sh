#!/usr/bin/env bash
# Daily backup for DFCHAT. Runs on the production host as root via cron.
# Backs up Postgres (logical dump) + MinIO bucket data + SRS DVR recordings
# + config files. Keeps 7 days of Postgres/config; MinIO/SRS use rsync mirror
# with --link-dest snapshots so unchanged files cost zero extra space.
set -euo pipefail

BACKUP_ROOT=/opt/dfchat/backups
DATA_ROOT=/opt/dfchat/data
KEEP_DAYS=7
DATE=$(date +%Y%m%d-%H%M%S)
TODAY=$(date +%Y%m%d)

mkdir -p "$BACKUP_ROOT"/{postgres,config,minio,srs}

log() { echo "[$(date '+%F %T')] $*"; }
fail() { log "FAIL: $*"; exit 1; }

# 1. Postgres logical dump
log "postgres: dumping"
set -a; . /opt/dfchat/.env; set +a
docker exec -e PGPASSWORD="$POSTGRES_PASSWORD" deploy-postgres-1 \
  pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-acl \
  | gzip -9 > "$BACKUP_ROOT/postgres/pg-$DATE.sql.gz" \
  || fail "pg_dump failed"
SIZE=$(stat -c%s "$BACKUP_ROOT/postgres/pg-$DATE.sql.gz")
[ "$SIZE" -lt 1024 ] && fail "pg dump too small ($SIZE bytes), likely empty"
log "postgres: $(numfmt --to=iec "$SIZE")"

# 2. Config files (env, compose, nginx, srs)
log "config: snapshotting"
tar -czf "$BACKUP_ROOT/config/config-$DATE.tar.gz" \
  -C / \
  opt/dfchat/.env \
  opt/dfchat/deploy/.env.prod \
  opt/dfchat/deploy/docker-compose.prod.yml \
  opt/dfchat/deploy/nginx.conf \
  opt/dfchat/deploy/srs.conf \
  2>/dev/null || log "config: some files missing (ok)"

# 3. MinIO data — rsync snapshot with hard-link dedup against yesterday
log "minio: syncing"
PREV_MINIO=$(ls -1d "$BACKUP_ROOT/minio/snap-"* 2>/dev/null | sort | tail -1 || true)
LINK_OPT_M=""
[ -n "$PREV_MINIO" ] && LINK_OPT_M="--link-dest=$PREV_MINIO"
rsync -a --delete $LINK_OPT_M "$DATA_ROOT/minio/" "$BACKUP_ROOT/minio/snap-$TODAY/" \
  || fail "minio rsync failed"

# 4. SRS DVR backup intentionally skipped — DVR is currently disabled in
# srs.conf (no replay UI). If you turn DVR back on, restore the rsync +
# trim block here.

# 5. Cleanup older than KEEP_DAYS
log "cleanup: pruning >$KEEP_DAYS days"
find "$BACKUP_ROOT/postgres" -name "pg-*.sql.gz" -mtime +$KEEP_DAYS -delete
find "$BACKUP_ROOT/config"   -name "config-*.tar.gz" -mtime +$KEEP_DAYS -delete
# For rsync snapshot dirs prune by name (mtime is unreliable due to hard links).
prune_snaps() {
  local dir="$1"
  ls -1d "$dir/snap-"* 2>/dev/null | sort | head -n -"$KEEP_DAYS" | xargs -r rm -rf
}
prune_snaps "$BACKUP_ROOT/minio"
# srs snapshots removed alongside the DVR drop above.

# 6. Report
log "done. usage:"
du -sh "$BACKUP_ROOT"/* 2>/dev/null | sed 's/^/  /'
