#!/usr/bin/env bash
# Close SSH port 22 after the new port has been verified.
# Run ON server02 (via the new port). Never run blindly — verify the new
# port works from your dev box FIRST.

set -euo pipefail
NEW_SSH_PORT=13494

echo "==> Verifying we're connected via the new port (not :22)"
# $SSH_CONNECTION = "<client-ip> <client-port> <server-ip> <server-port>"
server_port="${SSH_CONNECTION##* }"
if [ "$server_port" = "22" ]; then
  echo "❌ You are still connected over :22. Reconnect via :$NEW_SSH_PORT first."
  exit 1
fi
echo "  connected via :$server_port — ok"

echo "==> Rewriting /etc/ssh/sshd_config.d/10-dfchat.conf to drop port 22"
cat > /etc/ssh/sshd_config.d/10-dfchat.conf <<EOF
# DFCHAT: hardened — port 22 closed.
Port ${NEW_SSH_PORT}
PermitRootLogin yes
PasswordAuthentication yes
EOF
sshd -t
# Same socket-activation gotcha as bootstrap.sh — must restart the
# .socket unit, not the .service. systemctl reload ssh would silently
# do nothing visible at the socket layer.
systemctl daemon-reload
systemctl restart ssh.socket
sleep 1
if ss -tlnp | grep -q ':22 '; then
  echo "❌ :22 is still bound after restart — bailing without closing ufw"
  ss -tlnp | head; exit 1
fi
echo "  sshd now bound on :${NEW_SSH_PORT} only"

echo "==> Removing :22 from ufw"
ufw delete allow 22/tcp || true
ufw status numbered | head

echo ""
echo "✅ Port 22 closed. Only :${NEW_SSH_PORT} accepts SSH from now on."
echo "   Don't forget to update server02.rtf and .env.local on your dev box."
