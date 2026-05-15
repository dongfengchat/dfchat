#!/usr/bin/env bash
# One-shot deploy: generate prod .env locally, rsync the repo, build & up.
#
# Run from the project root on your dev machine:
#   bash deploy/deploy.sh
#
# Requires: rsync, sshpass, openssl in PATH on the dev machine.

set -euo pipefail
trap 'echo "❌ deploy failed on line $LINENO" >&2' ERR

HOST="${DFCHAT_HOST:-198.44.238.9}"
PORT="${DFCHAT_PORT:-13493}"
USER="${DFCHAT_USER:-root}"
PASSWORD="${DFCHAT_PASSWORD:?Set DFCHAT_PASSWORD env var}"
REMOTE_DIR="${DFCHAT_REMOTE_DIR:-/opt/dfchat}"

SSH="sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new ${USER}@${HOST}"
RSYNC_RSH="sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new"

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

# ---- 1. Generate prod .env if missing --------------------------------
ENV_PATH="deploy/.env.prod"
if [[ ! -f "$ENV_PATH" ]]; then
  echo "==> generating $ENV_PATH with fresh random secrets"
  rand() { openssl rand -base64 36 | tr -d '\n=+/' | cut -c1-32; }
  cat > "$ENV_PATH" <<EOF
# Auto-generated $(date -u +%FT%TZ). Keep this file secret.
POSTGRES_DB=dfchat
POSTGRES_USER=dfchat
POSTGRES_PASSWORD=$(rand)

REDIS_PASSWORD=$(rand)

MINIO_ROOT_USER=dfchat
MINIO_ROOT_PASSWORD=$(rand)
MINIO_PUBLIC_URL=http://${HOST}:9000

JWT_SECRET=$(openssl rand -base64 48 | tr -d '\n=+/' | cut -c1-48)
EOF
  chmod 600 "$ENV_PATH"
  echo "  -> wrote $ENV_PATH (mode 600). DO NOT COMMIT."
else
  echo "==> reusing existing $ENV_PATH"
fi

# ---- 2. rsync project to /opt/dfchat ---------------------------------
echo "==> rsync project to ${USER}@${HOST}:${REMOTE_DIR}"
$SSH "mkdir -p ${REMOTE_DIR}/data/postgres ${REMOTE_DIR}/data/redis ${REMOTE_DIR}/data/minio ${REMOTE_DIR}/data/letsencrypt"

rsync -az --delete \
  --exclude '.git' \
  --exclude 'client/node_modules' \
  --exclude 'client/dist' \
  --exclude 'client/dist-electron' \
  --exclude 'client/release' \
  --exclude 'server/bin' \
  --exclude 'server/tmp' \
  --exclude '*.log' \
  --exclude '/data/' \
  --exclude 'deploy/data/' \
  --exclude '.DS_Store' \
  --exclude '.env' \
  --exclude '.env.*' \
  -e "$RSYNC_RSH" \
  ./ "${USER}@${HOST}:${REMOTE_DIR}/"

# Place the generated .env at the expected location on the server.
$SSH "cp ${REMOTE_DIR}/deploy/.env.prod ${REMOTE_DIR}/.env && chmod 600 ${REMOTE_DIR}/.env"

# ---- 3. Build + bring up stack ---------------------------------------
echo "==> docker compose up -d --build (this builds the Go image)"
$SSH "cd ${REMOTE_DIR} && docker compose -f deploy/docker-compose.prod.yml --env-file .env up -d --build"

# ---- 4. Health check -------------------------------------------------
echo "==> waiting for /healthz"
for i in {1..60}; do
  if curl -sf --max-time 3 "http://${HOST}/healthz" >/dev/null 2>&1; then
    echo "  -> public /healthz OK"
    break
  fi
  sleep 2
done

$SSH "cd ${REMOTE_DIR} && docker compose -f deploy/docker-compose.prod.yml ps"

echo ""
echo "✅ deploy done"
echo ""
echo "Public endpoints:"
echo "  • API   http://${HOST}/healthz"
echo "  • API   http://${HOST}/api/v1/..."
echo "  • WS    ws://${HOST}/ws?token=..."
echo "  • MinIO http://${HOST}:9000/ (S3 endpoint, presigned)"
echo ""
echo "Secrets live at ${REMOTE_DIR}/.env  (chmod 600)."
echo "Local copy:   ${ENV_PATH}"
