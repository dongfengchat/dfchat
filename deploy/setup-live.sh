#!/usr/bin/env bash
# Stand up the live-streaming subsystem on the existing DFCHAT server.
#
# Idempotent — safe to re-run.
#
# What it does:
#   1. Add LIVE_* secrets to deploy/.env.prod (first run only)
#   2. rsync the project
#   3. Make sure those vars are present in the server-side /opt/dfchat/.env
#   4. UFW allow 1935 (RTMP ingest)
#   5. Create /opt/dfchat/data/srs (HLS + DVR landing zone)
#   6. Overwrite /opt/dfchat/deploy/nginx.conf with the full HTTPS config
#      that now also proxies /hls and /flv to the SRS container
#   7. Rebuild api image, start srs, recreate nginx
#   8. Smoke test

set -euo pipefail
trap 'echo "❌ setup-live failed on line $LINENO" >&2' ERR

HOST="${DFCHAT_HOST:-198.44.238.9}"
PORT="${DFCHAT_PORT:-13493}"
USER="${DFCHAT_USER:-root}"
PASSWORD="${DFCHAT_PASSWORD:?Set DFCHAT_PASSWORD env var (your server's SSH password)}"
REMOTE_DIR="${DFCHAT_REMOTE_DIR:-/opt/dfchat}"
BASE_DOMAIN="${BASE_DOMAIN:-dfchat.chat}"
APP_DOMAIN="${APP_DOMAIN:-app.${BASE_DOMAIN}}"
FILES_DOMAIN="${FILES_DOMAIN:-files.${BASE_DOMAIN}}"

SSH="sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new ${USER}@${HOST}"
RSYNC_RSH="sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new"

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

# ---- 1. Secrets ----------------------------------------------------
echo "==> 1/8 ensuring LIVE_* keys in deploy/.env.prod"
if ! grep -q '^LIVE_SRS_SECRET=' deploy/.env.prod 2>/dev/null; then
  SECRET=$(openssl rand -hex 24)
  cat >> deploy/.env.prod <<EOF

# Live streaming (RTMP/SRS)
LIVE_RTMP_URL=rtmp://${HOST}/live
LIVE_HLS_URL=https://${BASE_DOMAIN}/hls
LIVE_SRS_SECRET=${SECRET}
EOF
  echo "  generated LIVE_SRS_SECRET"
fi

# ---- 2. Push project ----------------------------------------------
echo "==> 2/8 rsync project"
rsync -az --delete \
  --exclude '.git' \
  --exclude 'client/node_modules' \
  --exclude 'client/dist' \
  --exclude 'client/dist-electron' \
  --exclude 'client/release' \
  --exclude '/data/' \
  --exclude 'deploy/data/' \
  --exclude '.DS_Store' \
  --exclude '.env' \
  --exclude '.env.*' \
  -e "$RSYNC_RSH" \
  ./ "${USER}@${HOST}:${REMOTE_DIR}/"

# Push deploy/.env.prod separately and copy to /opt/dfchat/.env (single
# source of truth for production credentials).
rsync -az -e "$RSYNC_RSH" deploy/.env.prod "${USER}@${HOST}:${REMOTE_DIR}/deploy/.env.prod"
$SSH "cp ${REMOTE_DIR}/deploy/.env.prod ${REMOTE_DIR}/.env && chmod 600 ${REMOTE_DIR}/.env"

# ---- 3. Server-side .env merge ------------------------------------
echo "==> 3/8 ensuring LIVE_* in server .env"
$SSH "for k in LIVE_RTMP_URL LIVE_HLS_URL LIVE_SRS_SECRET; do \
  if ! grep -q \"^\${k}=\" ${REMOTE_DIR}/.env; then \
    grep \"^\${k}=\" ${REMOTE_DIR}/deploy/.env.prod >> ${REMOTE_DIR}/.env; \
  fi; \
done && chmod 600 ${REMOTE_DIR}/.env"

# ---- 4. UFW -------------------------------------------------------
echo "==> 4/8 ufw allow 1935/tcp"
$SSH "ufw allow 1935/tcp comment 'rtmp ingest' >/dev/null 2>&1 && ufw reload >/dev/null"

# ---- 5. SRS data dir ---------------------------------------------
$SSH "mkdir -p ${REMOTE_DIR}/data/srs && chmod 755 ${REMOTE_DIR}/data/srs"

# ---- 6. Full nginx.conf rewrite (apex + app + files + live) -------
echo "==> 6/8 writing complete HTTPS nginx config"
$SSH "cat > ${REMOTE_DIR}/deploy/nginx.conf" <<NGINX
user  nginx;
worker_processes  auto;
events { worker_connections 4096; }

http {
  include       /etc/nginx/mime.types;
  default_type  application/octet-stream;
  sendfile on; tcp_nopush on; tcp_nodelay on;
  keepalive_timeout 75s;
  client_max_body_size 110m;

  # m3u8 should never be cached, ts can be cached briefly.
  types {
    application/vnd.apple.mpegurl m3u8;
    video/mp2t                    ts;
    application/vnd.apple.installer+xml dmg;
    application/x-msdownload             exe;
    application/x-executable             AppImage;
  }

  gzip on;
  gzip_types text/plain text/css application/javascript application/json image/svg+xml;
  gzip_min_length 1024;

  map \$http_upgrade \$connection_upgrade { default upgrade; '' close; }

  log_format combined_ms '\$remote_addr [\$time_local] "\$request" '
                         '\$status \$body_bytes_sent rt=\$request_time';
  access_log /var/log/nginx/access.log combined_ms;
  error_log  /var/log/nginx/error.log warn;

  upstream dfchat_api { server api:8080; }
  upstream dfchat_srs { server srs:8080; }

  # ---- :80 — ACME + force HTTPS ----
  server {
    listen 80 default_server;
    server_name _;
    location /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 301 https://\$host\$request_uri; }
  }

  # ---- :443 on apex (+ www) — website + downloads + HLS playback ----
  server {
    listen 443 ssl http2;
    server_name ${BASE_DOMAIN};

    ssl_certificate     /etc/letsencrypt/live/dfchat/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dfchat/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    add_header Strict-Transport-Security "max-age=31536000" always;

    root /var/www/dfchat/dist;
    index index.html;

    location /download/ {
      alias /var/www/dfchat/download/;
      add_header Content-Disposition "attachment" always;
      add_header Cache-Control "public, max-age=300" always;
      autoindex off;
    }

    # HLS m3u8 playlist — must NOT be cached (rotates every 2 seconds)
    location ~ ^/hls/(.+\.m3u8)\$ {
      proxy_pass http://dfchat_srs/live/\$1;
      proxy_set_header Host \$host;
      add_header Cache-Control "no-cache" always;
      add_header Access-Control-Allow-Origin "*" always;
    }
    # HLS ts segments — short cache is fine
    location ~ ^/hls/(.+\.ts)\$ {
      proxy_pass http://dfchat_srs/live/\$1;
      proxy_set_header Host \$host;
      add_header Cache-Control "public, max-age=10" always;
      add_header Access-Control-Allow-Origin "*" always;
    }
    # HTTP-FLV low-latency fallback
    location /flv/ {
      proxy_pass http://dfchat_srs/live/;
      proxy_set_header Host \$host;
      add_header Access-Control-Allow-Origin "*" always;
    }

    location / {
      try_files \$uri \$uri/ /index.html;
    }
  }

  # ---- :443 on app.<domain> — REST API + WebSocket ----
  server {
    listen 443 ssl http2;
    server_name ${APP_DOMAIN};

    ssl_certificate     /etc/letsencrypt/live/dfchat/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dfchat/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    add_header Strict-Transport-Security "max-age=31536000" always;

    add_header Access-Control-Allow-Origin "https://${BASE_DOMAIN}" always;
    add_header Access-Control-Allow-Methods "GET,POST,PATCH,DELETE,OPTIONS" always;
    add_header Access-Control-Allow-Headers "Content-Type,Authorization,X-Refresh-Token" always;
    if (\$request_method = OPTIONS) { return 204; }

    location /healthz { proxy_pass http://dfchat_api; }

    location /api/ {
      proxy_pass http://dfchat_api;
      proxy_http_version 1.1;
      proxy_set_header Host \$host;
      proxy_set_header X-Real-IP \$remote_addr;
      proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto \$scheme;
    }

    location /ws {
      proxy_pass http://dfchat_api;
      proxy_http_version 1.1;
      proxy_set_header Upgrade \$http_upgrade;
      proxy_set_header Connection \$connection_upgrade;
      proxy_set_header Host \$host;
      proxy_read_timeout 86400s;
      proxy_send_timeout 86400s;
    }

    location = / {
      add_header Content-Type "text/plain; charset=utf-8";
      return 200 "DFCHAT API.\nVisit https://${BASE_DOMAIN}/ for the website.\n";
    }
  }

  # ---- :443 on files.<domain> — MinIO is on :9000 directly ----
  server {
    listen 443 ssl http2;
    server_name ${FILES_DOMAIN};
    ssl_certificate     /etc/letsencrypt/live/dfchat/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dfchat/privkey.pem;
    location = / {
      add_header Content-Type "text/plain; charset=utf-8";
      return 200 "MinIO is on https://${FILES_DOMAIN}:9000/\n";
    }
  }
}
NGINX

# ---- 7. Rebuild + restart -----------------------------------------
echo "==> 7/8 rebuild api + start srs + recreate nginx"
$SSH "cd ${REMOTE_DIR} && \
  docker compose -f deploy/docker-compose.prod.yml --env-file .env up -d --build --force-recreate api srs nginx"

# ---- 8. Verify ----------------------------------------------------
echo "==> 8/8 verifying"
sleep 6
$SSH "echo '--- docker ps ---'; \
      docker compose -f ${REMOTE_DIR}/deploy/docker-compose.prod.yml --env-file ${REMOTE_DIR}/.env ps --format 'table {{.Name}}\t{{.Status}}'; \
      echo ''; \
      echo '--- migrations ---'; \
      docker compose -f ${REMOTE_DIR}/deploy/docker-compose.prod.yml --env-file ${REMOTE_DIR}/.env exec -T postgres \
        psql -U dfchat -d dfchat -tc \"SELECT version FROM schema_migrations\" 2>&1 | tail -3; \
      echo ''; \
      echo '--- public live list ---'; \
      curl -s --max-time 5 https://${BASE_DOMAIN}/api/v1/live/rooms | head -c 200; echo; \
      echo '--- SRS API ---'; \
      curl -s --max-time 3 http://127.0.0.1:1985/api/v1/versions | head -c 200; echo"

echo ""
echo "✅ live ready"
echo ""
echo "Try it:"
echo "  1. Log in via the desktop client, then:"
echo "       curl -X POST https://${APP_DOMAIN}/api/v1/live/rooms \\"
echo "            -H 'Authorization: Bearer <accessToken>' \\"
echo "            -H 'Content-Type: application/json' \\"
echo "            -d '{\"title\":\"测试直播\"}'"
echo "     → returns rtmpUrl + playbackUrl + streamKey"
echo "  2. OBS:  服务器 = rtmp://${HOST}/live    推流密钥 = <streamKey>"
echo "  3. Watch:  vlc/mpv https://${BASE_DOMAIN}/hls/<streamKey>.m3u8"
