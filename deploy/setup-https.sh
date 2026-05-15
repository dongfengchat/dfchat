#!/usr/bin/env bash
# Switch the DFCHAT stack from HTTP-only to HTTPS using Let's Encrypt.
#
#   bash deploy/setup-https.sh <base-domain> <email>
#       e.g.   bash deploy/setup-https.sh dfchat.chat me@example.com
#
# Requires the following DNS A records to already point at the server:
#     app.<base-domain>     → 198.44.238.9
#     files.<base-domain>   → 198.44.238.9
#
# Override the subdomain names with env vars if you used different ones:
#     APP_DOMAIN=chat.example.com FILES_DOMAIN=files.example.com bash ...

set -euo pipefail
trap 'echo "❌ setup-https failed on line $LINENO" >&2' ERR

BASE_DOMAIN="${1:?usage: bash deploy/setup-https.sh <base-domain> [email|none]}"
EMAIL="${2:-none}"

# Pass either --email <addr> --no-eff-email  or  --register-unsafely-without-email
# to certbot. With no email you simply won't get the 30/7-day expiry mails,
# which is fine since the cron job auto-renews anyway.
if [[ "$EMAIL" == "none" || "$EMAIL" == "-" || -z "$EMAIL" ]]; then
  CERTBOT_EMAIL_ARGS="--register-unsafely-without-email"
  echo "  email: (none — relying on auto-renewal)"
else
  CERTBOT_EMAIL_ARGS="--email ${EMAIL} --no-eff-email"
fi

APP_DOMAIN="${APP_DOMAIN:-app.${BASE_DOMAIN}}"
FILES_DOMAIN="${FILES_DOMAIN:-files.${BASE_DOMAIN}}"

HOST="${DFCHAT_HOST:-198.44.238.9}"
PORT="${DFCHAT_PORT:-13493}"
USER="${DFCHAT_USER:-root}"
PASSWORD="${DFCHAT_PASSWORD:?Set DFCHAT_PASSWORD env var (your server SSH password)}"
REMOTE_DIR="${DFCHAT_REMOTE_DIR:-/opt/dfchat}"

SSH="sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new ${USER}@${HOST}"

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "  app:   $APP_DOMAIN"
echo "  files: $FILES_DOMAIN"
echo "  server $HOST:$PORT"
echo ""

# ---- 1. DNS sanity check ---------------------------------------------
# Mac's default DNS can be hijacked (CN ISPs sometimes return placeholder
# IPs from 198.18.0.0/15). We bypass it by asking trusted public resolvers
# directly; if any one of them returns the expected IP, we accept.
echo "==> 1/8 verifying DNS"
RESOLVERS=(223.5.5.5 119.29.29.29 114.114.114.114 1.1.1.1 8.8.8.8)
check_dns() {
  local d="$1" expect="$2"
  for r in "${RESOLVERS[@]}"; do
    got=$(dig "@$r" +short +time=3 +tries=1 "$d" A 2>/dev/null | tail -1)
    if [[ "$got" == "$expect" ]]; then
      echo "  $d → $got  (via $r) ✓"
      return 0
    fi
  done
  return 1
}
for d in "$APP_DOMAIN" "$FILES_DOMAIN"; do
  if ! check_dns "$d" "$HOST"; then
    echo "❌ no resolver returned $HOST for $d"
    echo "   tried: ${RESOLVERS[*]}"
    echo "   add an A record: $d → $HOST  and wait for propagation."
    exit 1
  fi
done

# ---- 2. Push updated compose/nginx --------------------------------
echo "==> 2/8 pushing updated stack config"
rsync -az \
  -e "sshpass -p ${PASSWORD} ssh -p ${PORT} -o StrictHostKeyChecking=accept-new" \
  deploy/docker-compose.prod.yml deploy/nginx.conf \
  "${USER}@${HOST}:${REMOTE_DIR}/deploy/"

# ---- 3. Make sure nginx serves the ACME challenge dir --------------
echo "==> 3/8 ensuring nginx is up for ACME"
$SSH "mkdir -p ${REMOTE_DIR}/data/certbot-webroot ${REMOTE_DIR}/data/minio-certs && \
      cd ${REMOTE_DIR} && \
      docker compose -f deploy/docker-compose.prod.yml --env-file .env up -d --force-recreate nginx"
sleep 3

# ---- 4. Issue cert via certbot (one cert, two SANs) ----------------
echo "==> 4/8 requesting Let's Encrypt cert for $APP_DOMAIN + $FILES_DOMAIN"
$SSH "docker run --rm \
  -v ${REMOTE_DIR}/data/letsencrypt:/etc/letsencrypt \
  -v ${REMOTE_DIR}/data/certbot-webroot:/var/www/certbot \
  certbot/certbot certonly --webroot \
    -w /var/www/certbot \
    -d ${APP_DOMAIN} -d ${FILES_DOMAIN} \
    --cert-name dfchat \
    ${CERTBOT_EMAIL_ARGS} --agree-tos \
    --non-interactive --keep-until-expiring"

# ---- 5. Stage certs for MinIO --------------------------------------
echo "==> 5/8 staging MinIO TLS keys"
$SSH "cp -L ${REMOTE_DIR}/data/letsencrypt/live/dfchat/fullchain.pem ${REMOTE_DIR}/data/minio-certs/public.crt && \
      cp -L ${REMOTE_DIR}/data/letsencrypt/live/dfchat/privkey.pem  ${REMOTE_DIR}/data/minio-certs/private.key && \
      chmod 644 ${REMOTE_DIR}/data/minio-certs/public.crt && \
      chmod 600 ${REMOTE_DIR}/data/minio-certs/private.key"

# ---- 6. Write final HTTPS nginx.conf --------------------------------
echo "==> 6/8 writing HTTPS nginx config"
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

  map \$http_upgrade \$connection_upgrade { default upgrade; '' close; }

  log_format combined_ms '\$remote_addr [\$time_local] "\$request" '
                         '\$status \$body_bytes_sent rt=\$request_time';
  access_log /var/log/nginx/access.log combined_ms;
  error_log  /var/log/nginx/error.log warn;

  upstream dfchat_api { server api:8080; }

  # ---- :80 — ACME challenge + force HTTPS for everything ----
  server {
    listen 80 default_server;
    server_name _;
    location /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 301 https://\$host\$request_uri; }
  }

  # ---- :443 on app.<domain> — REST API + WebSocket ----
  server {
    listen 443 ssl http2;
    server_name ${APP_DOMAIN};

    ssl_certificate     /etc/letsencrypt/live/dfchat/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dfchat/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 10m;
    add_header Strict-Transport-Security "max-age=31536000" always;
    add_header X-Content-Type-Options nosniff always;

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
      proxy_set_header X-Real-IP \$remote_addr;
      proxy_read_timeout 86400s;
      proxy_send_timeout 86400s;
    }

    location = / {
      add_header Content-Type "text/plain; charset=utf-8";
      return 200 "DFCHAT API is running.\nUse the desktop client to connect.\n";
    }
  }

  # ---- :443 on files.<domain> — MinIO sits on :9000 directly, so
  # we don't proxy it; this block exists only to refuse stray :443 hits
  # to the files host so they don't get a default-server 444. ----
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

# ---- 7. Switch .env to HTTPS MinIO ----------------------------------
echo "==> 7/8 switching MinIO env to HTTPS"
$SSH "sed -i '/^MINIO_PUBLIC_URL=/d; /^MINIO_ENDPOINT=/d; /^MINIO_USE_SSL=/d' ${REMOTE_DIR}/.env && \
      printf 'MINIO_PUBLIC_URL=https://${FILES_DOMAIN}:9000\nMINIO_ENDPOINT=${FILES_DOMAIN}:9000\nMINIO_USE_SSL=true\n' >> ${REMOTE_DIR}/.env"

# Recreate minio (mounts certs now), api (env changed), nginx (new config).
$SSH "cd ${REMOTE_DIR} && \
      docker compose -f deploy/docker-compose.prod.yml --env-file .env up -d --force-recreate minio api nginx"

# ---- 8. Auto-renew cron ---------------------------------------------
echo "==> 8/8 installing daily renewal cron"
$SSH "cat > /etc/cron.daily/dfchat-cert-renew" <<RENEW
#!/usr/bin/env bash
# Renews the DFCHAT TLS cert if within 30 days of expiry, then syncs
# the renewed material to MinIO and restarts the affected services.
set -e
docker run --rm \\
  -v ${REMOTE_DIR}/data/letsencrypt:/etc/letsencrypt \\
  -v ${REMOTE_DIR}/data/certbot-webroot:/var/www/certbot \\
  certbot/certbot renew --webroot -w /var/www/certbot --quiet
LE_DIR=${REMOTE_DIR}/data/letsencrypt/live/dfchat
if [ -f \$LE_DIR/fullchain.pem ] && [ \$(find \$LE_DIR/fullchain.pem -mmin -5 | wc -l) -gt 0 ]; then
  cp -L \$LE_DIR/fullchain.pem ${REMOTE_DIR}/data/minio-certs/public.crt
  cp -L \$LE_DIR/privkey.pem  ${REMOTE_DIR}/data/minio-certs/private.key
  chmod 644 ${REMOTE_DIR}/data/minio-certs/public.crt
  chmod 600 ${REMOTE_DIR}/data/minio-certs/private.key
  cd ${REMOTE_DIR} && docker compose -f deploy/docker-compose.prod.yml --env-file .env restart minio nginx
fi
RENEW
$SSH "chmod +x /etc/cron.daily/dfchat-cert-renew"

# ---- Final smoke test ------------------------------------------------
# We run the curl FROM THE SERVER itself — your Mac's DNS may still be
# returning the hijacked IP locally even though everything is fine.
echo ""
echo "==> verifying from inside the server (bypasses Mac DNS hijack)"
sleep 5
$SSH "curl -sf --max-time 10 https://${APP_DOMAIN}/healthz && echo ''" || \
  echo "  ⚠️  https://${APP_DOMAIN}/healthz not yet responding — check 'docker compose logs nginx api'"
$SSH "curl -ks --max-time 10 -I https://${FILES_DOMAIN}:9000/minio/health/live | head -1" || true

echo ""
echo "✅ HTTPS up"
echo ""
echo "Public endpoints:"
echo "  • API   https://${APP_DOMAIN}/api/v1/..."
echo "  • WS    wss://${APP_DOMAIN}/ws?token=..."
echo "  • MinIO https://${FILES_DOMAIN}:9000/"
echo ""
echo "Update the client:"
echo "  client/.env.production:"
echo "      VITE_API_BASE=https://${APP_DOMAIN}"
echo "  Then: (cd client && npm run dist:mac)   # or dist:win / dist:linux"
echo ""
echo "Renewal: /etc/cron.daily/dfchat-cert-renew  (LE 60-day cycle, auto)."
