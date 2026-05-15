#!/usr/bin/env bash
# Cut a new release across all three platforms:
#   - macOS arm64 + x64 dmg  → built locally (needs a Mac host)
#   - Windows x64 .exe       → built on the prod server in a Linux+wine
#                              container (we can't run wine on Apple Silicon)
#   - Linux x64 AppImage     → built in the same container
# Then refresh the update manifest so existing clients see "new version"
# within 6h. The manifest now carries per-platform direct download URLs.
#
# Usage:
#   bash deploy/release.sh                 # rebuild & republish current version
#   bash deploy/release.sh 0.2.0           # bump → build all 3 → publish
#   bash deploy/release.sh 0.2.0 "新增 X 功能"
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"

PASS="${DFCHAT_PASSWORD:?Set DFCHAT_PASSWORD env var}"
HOST="${DFCHAT_HOST:-198.44.238.9}"
PORT="${DFCHAT_PORT:-13493}"
USER="${DFCHAT_USER:-root}"
RSH="sshpass -p $PASS ssh -p $PORT -o StrictHostKeyChecking=no"

NEW_VERSION="${1:-}"
NOTES="${2:-}"

cd "$ROOT/client"
if [ -n "$NEW_VERSION" ]; then
  echo "==> bumping to v$NEW_VERSION"
  npm version "$NEW_VERSION" --no-git-tag-version >/dev/null
fi
VERSION=$(node -p "require('./package.json').version")
echo "==> version: v$VERSION"

# ===== 1. macOS dmg (built locally) =====
echo "==> [mac] building dmg"
npm run dist:mac 2>&1 | tail -5

cd "$ROOT"
bash deploy/upload-binary.sh "client/release/DFCHAT-$VERSION-arm64.dmg" DFCHAT-mac-arm64.dmg | tail -2
bash deploy/upload-binary.sh "client/release/DFCHAT-$VERSION-x64.dmg" DFCHAT-mac-x64.dmg | tail -2

# ===== 2. Windows + Linux (built on server in wine container) =====
echo "==> [win+linux] syncing client src to server"
sshpass -p "$PASS" rsync -avz --delete \
  -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  --exclude 'node_modules' --exclude 'release' --exclude 'dist' \
  --exclude 'dist-electron' --exclude '.vite' \
  client/ "$USER@$HOST:/tmp/dfchat-build/client/" 2>&1 | tail -3

echo "==> [win+linux] building on server (this takes ~3-5 min)"
$RSH "$USER@$HOST" '
set -e
cd /tmp/dfchat-build/client
docker run --rm \
  -v "$(pwd):/project" \
  -v dfchat_electron_cache:/root/.cache/electron \
  -v dfchat_eb_cache:/root/.cache/electron-builder \
  -w /project \
  electronuserland/builder:wine \
  /bin/bash -c "
    npm install --no-audit --no-fund --loglevel=error 2>&1 | tail -3 && \
    npm run build 2>&1 | tail -3 && \
    npx electron-builder --win --x64 --linux --x64 2>&1 | tail -10
  "
mv release/*.exe       /opt/dfchat/web/download/DFCHAT-win-x64.exe
mv release/*.AppImage  /opt/dfchat/web/download/DFCHAT-linux.AppImage
chmod 644 /opt/dfchat/web/download/DFCHAT-win-x64.exe
chmod 755 /opt/dfchat/web/download/DFCHAT-linux.AppImage
ls -lh /opt/dfchat/web/download/
'

# ===== 3. Update manifest (per-platform direct links) =====
echo "==> refreshing update manifest"
TMP=$(mktemp -t latest.XXXXXX.json)
cat > "$TMP" <<EOF
{
  "version": "$VERSION",
  "releasedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "downloads": {
    "darwin-arm64": "https://dfchat.chat/download/DFCHAT-mac-arm64.dmg",
    "darwin-x64":   "https://dfchat.chat/download/DFCHAT-mac-x64.dmg",
    "win32-x64":    "https://dfchat.chat/download/DFCHAT-win-x64.exe",
    "linux-x64":    "https://dfchat.chat/download/DFCHAT-linux.AppImage"
  },
  "downloadUrl": "https://dfchat.chat/#download",
  "notes": "${NOTES//\"/\\\"}"
}
EOF
chmod 644 "$TMP"  # rsync preserves perms; mktemp defaults to 600 (nginx 403)

$RSH "$USER@$HOST" "mkdir -p /opt/dfchat/web/updates /opt/dfchat/web/dist/updates"
sshpass -p "$PASS" rsync -avz -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  "$TMP" "$USER@$HOST:/opt/dfchat/web/updates/latest.json" | tail -2
sshpass -p "$PASS" rsync -avz -e "ssh -p $PORT -o StrictHostKeyChecking=no" \
  "$TMP" "$USER@$HOST:/opt/dfchat/web/dist/updates/latest.json" | tail -2
rm "$TMP"

echo ""
echo "✅ v$VERSION released to all 3 platforms. Existing clients see banner within 6h."
echo "   Live manifest:"
curl -s "https://dfchat.chat/updates/latest.json"
echo ""
