#!/usr/bin/env bash
# Deploy backup + cert-renewal automation to the production host.
# Idempotent — safe to re-run.
set -euo pipefail

SERVER=${SERVER:-198.44.238.9}
SSH_PORT=${SSH_PORT:-13493}
SSH_USER=${SSH_USER:-root}
SSH_PASS=${SSH_PASS:?Set SSH_PASS env var}

SSH="sshpass -p $SSH_PASS ssh -o StrictHostKeyChecking=no -p $SSH_PORT $SSH_USER@$SERVER"
RSYNC_RSH="sshpass -p $SSH_PASS ssh -o StrictHostKeyChecking=no -p $SSH_PORT"

HERE=$(cd "$(dirname "$0")" && pwd)

echo "==> 1. Push scripts to /opt/dfchat/scripts/"
$SSH "mkdir -p /opt/dfchat/scripts"
rsync -avz -e "$RSYNC_RSH" \
  "$HERE/scripts/" "$SSH_USER@$SERVER:/opt/dfchat/scripts/"
$SSH "chmod +x /opt/dfchat/scripts/*.sh"

echo "==> 2. Register webroot renewal config (idempotent)"
# Current cert was issued with --standalone, so renewal/ is empty.
# Running certonly --webroot with --keep-until-expiring writes the
# renewal config without actually re-issuing (cert still has >60 days).
$SSH 'docker run --rm \
    -v /opt/dfchat/data/letsencrypt:/etc/letsencrypt \
    -v /opt/dfchat/data/certbot-webroot:/var/www/certbot \
    certbot/certbot:latest \
    certonly --webroot -w /var/www/certbot \
      -d dfchat.chat -d app.dfchat.chat -d files.dfchat.chat \
      --cert-name dfchat \
      --non-interactive --agree-tos --register-unsafely-without-email \
      --keep-until-expiring 2>&1 | tail -20'

echo "==> 3. Verify renewal config now exists"
$SSH 'ls -la /opt/dfchat/data/letsencrypt/renewal/'

echo "==> 4. Install crontab (3:15 backup, 4:30 cert renew check)"
$SSH 'crontab -l 2>/dev/null | grep -v dfchat- > /tmp/crontab.new || true
cat >> /tmp/crontab.new <<EOF
15 3 * * * /opt/dfchat/scripts/backup.sh >> /var/log/dfchat-backup.log 2>&1 # dfchat-backup
30 4 * * * /opt/dfchat/scripts/cert-renew.sh >> /var/log/dfchat-cert.log 2>&1 # dfchat-cert
EOF
crontab /tmp/crontab.new
rm /tmp/crontab.new
echo "--- installed crontab ---"
crontab -l'

echo "==> 5. First backup run (to verify)"
$SSH '/opt/dfchat/scripts/backup.sh 2>&1 | tee /var/log/dfchat-backup.log | tail -30'

echo "==> 6. certbot --dry-run renewal check"
$SSH '/opt/dfchat/scripts/cert-renew.sh --dry-run 2>&1 | tail -20'

echo "==> 7. Final state"
$SSH 'echo "--- backups ---"; du -sh /opt/dfchat/backups/*; echo "--- cert ---"; openssl x509 -in /opt/dfchat/data/letsencrypt/live/dfchat/cert.pem -noout -dates'

echo "==> Done."
