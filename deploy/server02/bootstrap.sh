#!/usr/bin/env bash
# DFCHAT — server02 first-time bootstrap (Ubuntu 24.04).
#
# Idempotent. Safe to re-run.
#
# What it does:
#   1. Move SSH from :22 to :13494, keeping :22 open as fallback for one
#      hop so we don't lock ourselves out. Caller must then verify the
#      new port works and follow up by running close-22.sh manually.
#   2. Install docker engine + compose plugin.
#   3. Install rsync, sshpass not needed (we connect FROM dev box).
#   4. ufw firewall: allow new SSH port, RTMP 1935, HTTP 80, HTTPS 443.
#      DENY old 22 only via the separate close-22.sh after verification.
#   5. Create /opt/dfchat-live data dirs with the right permissions.
#
# Run this AS ROOT on server02:
#     bash bootstrap.sh
#
# After it finishes:
#     # 1. From your dev box, verify the new SSH port works:
#     ssh -p 13494 root@45.119.4.109 'echo ok'
#     # 2. If OK, close port 22:
#     ssh -p 13494 root@45.119.4.109 'bash /opt/dfchat-live/deploy/close-22.sh'
#     # 3. Update server02.rtf locally to reflect port 13494.

set -euo pipefail
trap 'echo "❌ bootstrap failed at line $LINENO" >&2' ERR

NEW_SSH_PORT=13494

echo "==> 1/5  Adjusting SSH config (add port $NEW_SSH_PORT alongside 22)"
# Use a drop-in file rather than editing /etc/ssh/sshd_config — survives
# package upgrades cleanly and doesn't fight with the distro's defaults.
mkdir -p /etc/ssh/sshd_config.d
cat > /etc/ssh/sshd_config.d/10-dfchat.conf <<EOF
# DFCHAT: added by deploy/server02/bootstrap.sh
# Keep :22 open during cutover, then drop via close-22.sh.
Port 22
Port ${NEW_SSH_PORT}
PermitRootLogin yes
PasswordAuthentication yes
EOF
# Validate before activating so a typo doesn't take SSH down.
sshd -t
# Ubuntu 24.04 uses systemd socket activation for ssh — sshd-socket-generator
# regenerates /run/systemd/generator/ssh.socket.d/addresses.conf from the
# `Port` lines in sshd_config, but the *running* ssh.socket unit has the
# old port set baked in. A plain `systemctl reload ssh` reloads the
# service config but NOT the socket. Result: new port appears in the
# generator output but nothing's listening on it. Force the socket to
# restart so it re-reads the generated drop-in.
systemctl daemon-reload
systemctl restart ssh.socket
# Sanity: confirm both ports are now bound.
sleep 1
if ! ss -tlnp 2>/dev/null | grep -q ":${NEW_SSH_PORT} "; then
  echo "❌ sshd not listening on ${NEW_SSH_PORT} after socket restart"
  ss -tlnp | head; exit 1
fi
echo "  sshd now bound on :22 AND :${NEW_SSH_PORT}"

echo "==> 2/5  Installing docker engine + compose plugin"
if ! command -v docker >/dev/null 2>&1; then
  apt-get update -y
  apt-get install -y ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi

echo "==> 2b/5  Configuring docker registry mirrors (China-friendly)"
# Direct pulls from auth.docker.io reset connection roughly 1 in 5 from
# this host. Mirror list is rotated quarterly; if all four break, run
# `docker info | grep -A5 Mirrors` and swap in fresher ones from
# https://github.com/dongyubin/DockerHub.
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'JSON'
{
  "registry-mirrors": [
    "https://docker.m.daocloud.io",
    "https://docker.1ms.run",
    "https://hub.rat.dev",
    "https://docker.imgdb.de"
  ],
  "max-concurrent-downloads": 3
}
JSON
systemctl restart docker

echo "==> 3/5  Installing helpers (rsync, fail2ban, certbot stays in container)"
apt-get install -y rsync fail2ban ca-certificates curl

echo "==> 4/5  Configuring ufw firewall"
apt-get install -y ufw
# Defaults: deny incoming, allow outgoing.
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp        comment 'ssh fallback during cutover'
ufw allow ${NEW_SSH_PORT}/tcp  comment 'ssh new'
ufw allow 80/tcp        comment 'http acme + redirect'
ufw allow 443/tcp       comment 'https hls'
ufw allow 1935/tcp      comment 'rtmp ingest'
ufw --force enable

echo "==> 5/5  Creating /opt/dfchat-live data layout"
mkdir -p /opt/dfchat-live/{data/srs,data/letsencrypt,data/certbot-webroot,deploy}
# SRS runs as root inside the container — these can be 755.
chmod 755 /opt/dfchat-live /opt/dfchat-live/data
chmod 700 /opt/dfchat-live/data/letsencrypt

echo ""
echo "✅ bootstrap done"
echo ""
echo "Next:"
echo "  1. From dev box: ssh -p ${NEW_SSH_PORT} root@<this-ip> 'echo ok'"
echo "  2. If OK, run on server02 to close :22:"
echo "        bash /opt/dfchat-live/deploy/close-22.sh"
echo "  3. Run deploy.sh from dev box to ship configs + start services."
