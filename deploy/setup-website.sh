#!/usr/bin/env bash
# Add the apex (and optional www) domain to the existing LE cert, serve the
# static marketing site + /download/ binaries from nginx.
#
#   bash deploy/setup-website.sh dfchat.chat
#
# Pre-requisites:
#   1. setup-https.sh already ran (cert for app.+files. already exists)
#   2. DNS A: dfchat.chat → 198.44.238.9   (and optionally www. → same)
#   3. /opt/dfchat/web/ will be filled by deploy-web.sh

set -euo pipefail
trap 'echo "❌ setup-website failed on line $LINENO" >&2' ERR

BASE_DOMAIN="${1:?usage: bash deploy/setup-website.sh <base-domain>}"
APP_DOMAIN="${APP_DOMAIN:-app.${BASE_DOMAIN}}"
FILES_DOMAIN="${FILES_DOMAIN:-files.${BASE_DOMAIN}}"
WWW_DOMAIN="www.${BASE_DOMAIN}"

HOST="${DFCHAT_HOST:-198.44.238.9}"
PORT="${DFCHAT_PORT:-13493}"
USER="${DFCHAT_USER:-root}"
PASSWORD="${DFCHAT_PASSWORD:?Set DFCHAT_PASSWORD env var (your server SSH password)}"
REMOTE_DIR="${DFCHAT_REMOTE_DIR:-/opt/dfchat}"

SSH="sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new ${USER}@${HOST}"

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

# ---- 1. DNS check (apex + optional www) ------------------------------
RESOLVERS=(223.5.5.5 119.29.29.29 114.114.114.114 1.1.1.1 8.8.8.8)
check_dns() {
  local d="$1" expect="$2"
  for r in "${RESOLVERS[@]}"; do
    got=$(dig "@$r" +short +time=3 +tries=1 "$d" A 2>/dev/null | tail -1)
    [[ "$got" == "$expect" ]] && { echo "  $d → $got  (via $r) ✓"; return 0; }
  done
  return 1
}

echo "==> 1/5 verifying DNS"
if ! check_dns "$BASE_DOMAIN" "$HOST"; then
  echo "❌ no resolver returned $HOST for $BASE_DOMAIN"
  echo "   add an A record:  @ (apex) $BASE_DOMAIN → $HOST"
  exit 1
fi
WWW_OK=0
if check_dns "$WWW_DOMAIN" "$HOST" 2>/dev/null; then
  WWW_OK=1
fi
[[ $WWW_OK -eq 0 ]] && echo "  (note: $WWW_DOMAIN not configured — skipping)"

# ---- 2. Expand LE cert to also cover apex (+ optional www) ----------
# certbot expand keeps the same cert name "dfchat" so nginx paths don't change.
EXPAND_DOMS=("-d" "$APP_DOMAIN" "-d" "$FILES_DOMAIN" "-d" "$BASE_DOMAIN")
if [[ $WWW_OK -eq 1 ]]; then EXPAND_DOMS+=("-d" "$WWW_DOMAIN"); fi

echo "==> 2/5 expanding cert to include $BASE_DOMAIN$([[ $WWW_OK -eq 1 ]] && echo " + $WWW_DOMAIN")"
$SSH "docker run --rm \
  -v ${REMOTE_DIR}/data/letsencrypt:/etc/letsencrypt \
  -v ${REMOTE_DIR}/data/certbot-webroot:/var/www/certbot \
  certbot/certbot certonly --webroot \
    -w /var/www/certbot \
    ${EXPAND_DOMS[*]} \
    --cert-name dfchat \
    --register-unsafely-without-email --agree-tos \
    --non-interactive --expand"

# Re-stage MinIO certs (cert files refreshed by --expand).
$SSH "cp -L ${REMOTE_DIR}/data/letsencrypt/live/dfchat/fullchain.pem ${REMOTE_DIR}/data/minio-certs/public.crt && \
      cp -L ${REMOTE_DIR}/data/letsencrypt/live/dfchat/privkey.pem  ${REMOTE_DIR}/data/minio-certs/private.key && \
      chmod 644 ${REMOTE_DIR}/data/minio-certs/public.crt && \
      chmod 600 ${REMOTE_DIR}/data/minio-certs/private.key"

# ---- 3. Push the static site to /opt/dfchat/web/ --------------------
# nginx serves from /var/www/dfchat (mount of /opt/dfchat/web). We used
# to nest a /dist subdir here but deploy.sh's project-wide rsync would
# overwrite it to the local web/ flat layout — so keep flat throughout.
echo "==> 3/5 rsync website"
$SSH "mkdir -p ${REMOTE_DIR}/web/download"
rsync -az --delete \
  -e "sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new" \
  --exclude '/download/' \
  web/ "${USER}@${HOST}:${REMOTE_DIR}/web/"

# ---- 4. Write nginx config with apex server block + /download/ ------
SERVER_NAMES="$BASE_DOMAIN"
[[ $WWW_OK -eq 1 ]] && SERVER_NAMES="$BASE_DOMAIN $WWW_DOMAIN"

echo "==> 4/5 writing nginx config (3 server blocks: apex / app / files)"
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

  # Bigger download buffers so DMGs stream without stalls.
  proxy_buffering off;

  # Make .dmg, .exe, .AppImage download instead of trying to render.
  types {
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

  # ---- :80 — ACME + force HTTPS ----
  server {
    listen 80 default_server;
    server_name _;
    location /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 301 https://\$host\$request_uri; }
  }

  # ---- :443 on apex (+ www) — marketing site + /download/ ----
  server {
    listen 443 ssl http2;
    server_name ${SERVER_NAMES};

    ssl_certificate     /etc/letsencrypt/live/dfchat/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dfchat/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    add_header Strict-Transport-Security "max-age=31536000" always;
    add_header X-Content-Type-Options nosniff always;

    root /var/www/dfchat;
    index index.html;

    # /download/<filename> — installer binaries served with attachment header
    # so the browser triggers a download instead of trying to inline them.
    location /download/ {
      alias /var/www/dfchat/download/;
      add_header Content-Disposition "attachment" always;
      add_header Cache-Control "public, max-age=300" always;
      autoindex off;
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
    add_header X-Content-Type-Options nosniff always;

    # Allow the marketing site (on apex) to call the API from JS without
    # a CORS preflight failure.
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

  # ---- :443 on files.<domain> — MinIO is on :9000, this is just a stub ----
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

# Push the latest docker-compose.prod.yml (which already has the
# /opt/dfchat/web bind mount baked in) and recreate nginx.
rsync -az \
  -e "sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new" \
  deploy/docker-compose.prod.yml \
  "${USER}@${HOST}:${REMOTE_DIR}/deploy/docker-compose.prod.yml"

$SSH "cd ${REMOTE_DIR} && docker compose -f deploy/docker-compose.prod.yml --env-file .env up -d --force-recreate nginx"

# ---- 5. Smoke test --------------------------------------------------
echo "==> 5/5 verifying"
sleep 3
$SSH "echo '--- apex landing ---'; \
      curl -sI --max-time 8 https://${BASE_DOMAIN}/ | head -3; \
      echo ''; \
      echo '--- api still OK ---'; \
      curl -s --max-time 8 https://${APP_DOMAIN}/healthz; echo; \
      echo '--- cert SAN ---'; \
      echo | openssl s_client -servername ${BASE_DOMAIN} -connect ${BASE_DOMAIN}:443 2>/dev/null | \
        openssl x509 -noout -ext subjectAltName 2>/dev/null"

echo ""
echo "✅ Website live at https://${BASE_DOMAIN}/"
echo ""
echo "Upload installer binaries to /opt/dfchat/web/download/ like:"
echo "  bash deploy/upload-binary.sh client/release/DFCHAT-0.1.0-arm64.dmg DFCHAT-mac-arm64.dmg"
