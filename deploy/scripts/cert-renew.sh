#!/usr/bin/env bash
# Daily Let's Encrypt renewal for DFCHAT. Idempotent: certbot only renews
# when <30 days remain. If a renewal happened, the deploy hooks below
# re-stage the MinIO cert and reload nginx.
#
# Uses webroot mode (nginx is up 24/7 and already serves
# /.well-known/acme-challenge/ from /var/www/certbot).
set -euo pipefail

LE_DIR=/opt/dfchat/data/letsencrypt
WEBROOT=/opt/dfchat/data/certbot-webroot
CERT_NAME=dfchat
LIVE_CERT="$LE_DIR/live/$CERT_NAME/fullchain.pem"

log() { echo "[$(date '+%F %T')] $*"; }

# Hash before, to detect actual renewal
PRE_HASH=""
[ -f "$LIVE_CERT" ] && PRE_HASH=$(md5sum "$LIVE_CERT" | awk '{print $1}')

# Allow caller to pass --dry-run for testing
EXTRA="${1:-}"

log "renew: starting (extra=$EXTRA)"
docker run --rm \
  -v "$LE_DIR:/etc/letsencrypt" \
  -v "$WEBROOT:/var/www/certbot" \
  certbot/certbot:latest \
  renew --webroot -w /var/www/certbot --non-interactive --quiet $EXTRA \
  || { log "renew: certbot exited non-zero"; exit 1; }

if [ "$EXTRA" = "--dry-run" ]; then
  log "renew: dry-run OK"
  exit 0
fi

POST_HASH=""
[ -f "$LIVE_CERT" ] && POST_HASH=$(md5sum "$LIVE_CERT" | awk '{print $1}')

if [ "$PRE_HASH" = "$POST_HASH" ]; then
  log "renew: cert unchanged, nothing to deploy"
  exit 0
fi

log "renew: cert changed, deploying"

# MinIO: copy + restart (MinIO reads certs on startup only)
log "deploy: minio cert + restart"
install -m 644 "$LE_DIR/live/$CERT_NAME/fullchain.pem" /opt/dfchat/data/minio-certs/public.crt
install -m 600 "$LE_DIR/live/$CERT_NAME/privkey.pem"   /opt/dfchat/data/minio-certs/private.key
docker restart deploy-minio-1 >/dev/null

# Nginx: hot reload (no downtime)
log "deploy: nginx reload"
docker exec deploy-nginx-1 nginx -s reload

log "deploy: done"
