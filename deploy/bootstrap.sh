#!/usr/bin/env bash
# DFCHAT server bootstrap — run once on a clean Ubuntu 20.04 host.
#
# Installs Docker + Compose plugin, sets up UFW, makes the /opt/dfchat
# project directory. Idempotent — safe to re-run.

set -euo pipefail
trap 'echo "❌ bootstrap failed on line $LINENO" >&2' ERR

SSH_PORT="${SSH_PORT:-13493}"

echo "==> apt update + base tools"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
  ca-certificates curl gnupg lsb-release ufw rsync git jq \
  software-properties-common apt-transport-https

# ---- Docker -----------------------------------------------------------
if ! command -v docker >/dev/null; then
  echo "==> installing docker engine + compose plugin"
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  ARCH=$(dpkg --print-architecture)
  CODENAME=$(lsb_release -cs)
  echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io \
    docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
fi

docker --version
docker compose version

# ---- Firewall ---------------------------------------------------------
echo "==> configuring UFW"
ufw default deny incoming
ufw default allow outgoing
ufw allow "${SSH_PORT}/tcp" comment "ssh"
ufw allow 80/tcp   comment "http"
ufw allow 443/tcp  comment "https (future)"
ufw allow 9000/tcp comment "minio s3 (presigned uploads)"
# Internal services (Postgres/Redis/NATS) intentionally NOT opened —
# everything that needs them lives inside the docker network.
ufw --force enable
ufw status verbose

# ---- Swappiness tuning for a chat workload ---------------------------
sysctl -w vm.swappiness=10 >/dev/null
grep -q '^vm.swappiness' /etc/sysctl.conf \
  && sed -i 's/^vm.swappiness.*/vm.swappiness = 10/' /etc/sysctl.conf \
  || echo 'vm.swappiness = 10' >> /etc/sysctl.conf

# ---- Filesystem layout -----------------------------------------------
mkdir -p /opt/dfchat /opt/dfchat/data/postgres /opt/dfchat/data/redis /opt/dfchat/data/minio
chmod 700 /opt/dfchat/data

# ---- Time sync (chat needs consistent clocks for tokens/seq) ----------
timedatectl set-ntp true || true

echo ""
echo "✅ bootstrap done"
echo ""
echo "Next:  rsync the project to /opt/dfchat/  (instructions on the dev machine)"
