#!/usr/bin/env bash
# Issue the live.dfchat.chat TLS cert via acme.sh installed natively.
#
# We use acme.sh installed on the host (not in a container) because:
#   1. Python TLS fingerprint (certbot) gets connection-reset by GFW
#      on this Chinese-network host — verified via direct test.
#   2. The neilpang/acme.sh Docker image fails to pull through any of
#      the configured mirrors (Cloudflare CDN blobs reset mid-stream).
#   3. acme.sh is just a shell script that uses /usr/bin/curl, which
#      works perfectly from this host's network.
#
# Run AFTER:
#   - DNS A record for live.dfchat.chat → this server's IP has propagated
#   - bootstrap.sh has run
#   - The nginx container is running on :80 with the ACME location wired
#
# Run ON server02:
#     bash /opt/dfchat-live/deploy/issue-cert.sh you@example.com
#
# Cert is renewed automatically via cron — acme.sh's installer drops a
# crontab entry that runs `acme.sh --cron` daily. Renewal happens when
# <30 days remain on the cert, then the install-cert reloadcmd reloads
# nginx via docker compose exec.

set -euo pipefail
EMAIL="${1:?usage: bash issue-cert.sh <admin-email>}"
DOMAIN="live.dfchat.chat"
NGINX_LIVE_DIR="/opt/dfchat-live/data/letsencrypt/live/$DOMAIN"
WEBROOT="/opt/dfchat-live/data/certbot-webroot"
COMPOSE="docker compose --env-file /opt/dfchat-live/.env -f /opt/dfchat-live/deploy/docker-compose.yml"

# Pre-check: DNS resolves to us. Without this the ACME http-01 challenge
# lands at someone else's server and fails with "Invalid response".
my_ip=$(curl -s --max-time 5 https://api.ipify.org || echo "")
dns_ip=$(dig +short "$DOMAIN" A | tail -1)
if [ -z "$dns_ip" ]; then
  echo "❌ $DOMAIN does not resolve. Add an A record first."; exit 1
fi
if [ -n "$my_ip" ] && [ "$my_ip" != "$dns_ip" ]; then
  echo "❌ $DOMAIN resolves to $dns_ip but our public IP is $my_ip."; exit 1
fi
echo "==> $DOMAIN → $dns_ip (matches this host)"

# Install acme.sh if not already installed. The installer is idempotent.
if [ ! -x "$HOME/.acme.sh/acme.sh" ]; then
  echo "==> installing acme.sh"
  curl -fsSL https://get.acme.sh | sh -s "email=$EMAIL"
fi

ACME=$HOME/.acme.sh/acme.sh
$ACME --set-default-ca --server letsencrypt >/dev/null

echo "==> issuing cert (will retry up to 5x — finalize POST is flaky on this network)"
issued=0
for i in 1 2 3 4 5; do
  if $ACME --issue -d "$DOMAIN" --webroot "$WEBROOT" --keylength 2048 --force 2>&1 | tail -8 \
     || $ACME --renew -d "$DOMAIN" --force 2>&1 | tail -8; then
    # acme.sh returns non-zero even on partial success (challenge ok,
    # finalize 500). Check for the actual cert file instead.
    if [ -s "$HOME/.acme.sh/$DOMAIN/fullchain.cer" ]; then
      issued=1
      echo "  ✓ cert present after attempt $i"
      break
    fi
  fi
  echo "  attempt $i didn't produce a cert; retrying"
  sleep 5
done
[ "$issued" = 1 ] || { echo "❌ all 5 attempts failed; check ~/.acme.sh/acme.sh.log"; exit 1; }

echo "==> installing cert to nginx path + wiring reloadcmd"
mkdir -p "$NGINX_LIVE_DIR"
$ACME --install-cert -d "$DOMAIN" \
  --key-file       "$NGINX_LIVE_DIR/privkey.pem" \
  --fullchain-file "$NGINX_LIVE_DIR/fullchain.pem" \
  --reloadcmd      "$COMPOSE exec -T nginx nginx -s reload"

echo "==> verifying cert is the real LE one, not the bootstrap self-signed"
openssl x509 -in "$NGINX_LIVE_DIR/fullchain.pem" -noout -issuer -dates

echo
echo "✅ done"
echo "   Test from anywhere: curl -sI https://$DOMAIN/healthz"
echo "   Auto-renewal: acme.sh's installer added a daily cron; run"
echo "                  '$ACME --list' to see the schedule."
