#!/usr/bin/env bash
# Push a built installer to /opt/dfchat/web/download/ so the website's
# download buttons can serve it.
#
#   bash deploy/upload-binary.sh <local-path> [<remote-filename>]
#
# Example:
#   bash deploy/upload-binary.sh client/release/DFCHAT-0.1.0-arm64.dmg DFCHAT-mac-arm64.dmg

set -euo pipefail

LOCAL="${1:?usage: bash deploy/upload-binary.sh <local-path> [<remote-name>]}"
REMOTE_NAME="${2:-$(basename "$LOCAL")}"

HOST="${DFCHAT_HOST:-198.44.238.9}"
PORT="${DFCHAT_PORT:-13493}"
USER="${DFCHAT_USER:-root}"
PASSWORD="${DFCHAT_PASSWORD:?Set DFCHAT_PASSWORD env var (your server's SSH password)}"

if [[ ! -f "$LOCAL" ]]; then
  echo "❌ file not found: $LOCAL"; exit 1
fi

size=$(du -h "$LOCAL" | cut -f1)
echo "==> uploading $LOCAL ($size) → /opt/dfchat/web/download/$REMOTE_NAME"

# Make sure the destination tree exists. setup-website.sh creates this,
# but we want this script standalone for the upload-only workflow.
sshpass -p "${PASSWORD}" ssh -p "${PORT}" -o StrictHostKeyChecking=accept-new \
  "${USER}@${HOST}" "mkdir -p /opt/dfchat/web/download"

rsync -avh --progress \
  -e "sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new" \
  "$LOCAL" "${USER}@${HOST}:/opt/dfchat/web/download/${REMOTE_NAME}"

echo ""
echo "✅ live at: https://dfchat.chat/download/${REMOTE_NAME}"
