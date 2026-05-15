#!/usr/bin/env bash
# Lightweight monitoring + alerting. Runs every 5 min via cron.
#
# Checks:
#   1. https://app.dfchat.chat/healthz returns HTTP 200 (deep probe: PG+MinIO+SRS)
#   2. Root filesystem usage < $DISK_THRESHOLD (default 80%)
#   3. All expected docker containers are running
#
# Behavior:
#   - First failure transitions OK → FAIL → push alert to $DFCHAT_ALERT_WEBHOOK
#   - Persistent FAIL: no re-spam (avoids notification storms)
#   - Recovery transitions FAIL → OK → push "recovered" alert
#   - State persists in /tmp/dfchat-monitor.state
#
# Webhook payload uses the "text" shape that works for both 企业微信 群机器人
# and 钉钉 自定义机器人. Set DFCHAT_ALERT_WEBHOOK in /opt/dfchat/.env to
# enable. Empty = log-only mode (still useful via journalctl / log file).
set -euo pipefail

# Load env (provides DFCHAT_ALERT_WEBHOOK if user configured it).
set -a; . /opt/dfchat/.env 2>/dev/null || true; set +a

DISK_THRESHOLD=${DISK_THRESHOLD:-80}
HEALTH_URL=${DFCHAT_HEALTH_URL:-https://app.dfchat.chat/healthz}
EXPECTED_CONTAINERS=(deploy-api-1 deploy-postgres-1 deploy-redis-1 deploy-minio-1 deploy-nginx-1 deploy-srs-1 deploy-coturn-1)
STATE_FILE=/tmp/dfchat-monitor.state

log() { echo "[$(date '+%F %T')] $*"; }

# === probes ===
reasons=()

http_code=$(curl -s -o /tmp/dfchat-healthz.body -w "%{http_code}" --max-time 6 "$HEALTH_URL" || echo "000")
if [ "$http_code" != "200" ]; then
  body_snip=$(head -c 200 /tmp/dfchat-healthz.body 2>/dev/null || echo "")
  reasons+=("healthz HTTP $http_code · body: $body_snip")
fi

disk_pct=$(df / | awk 'NR==2 {print $5}' | tr -d '%')
if [ "$disk_pct" -gt "$DISK_THRESHOLD" ]; then
  reasons+=("disk usage ${disk_pct}% > ${DISK_THRESHOLD}%")
fi

for c in "${EXPECTED_CONTAINERS[@]}"; do
  if ! docker ps --format "{{.Names}}" | grep -q "^${c}$"; then
    reasons+=("container down: $c")
  fi
done

# === state transition ===
PREV_STATE=$(cat "$STATE_FILE" 2>/dev/null || echo OK)
NEW_STATE=OK
[ ${#reasons[@]} -gt 0 ] && NEW_STATE=FAIL

log "state=$NEW_STATE prev=$PREV_STATE reasons=[$(IFS=,; echo "${reasons[*]:-}")]"

echo "$NEW_STATE" > "$STATE_FILE"

if [ "$NEW_STATE" = "$PREV_STATE" ]; then
  exit 0  # no transition, no alert
fi

if [ -z "${DFCHAT_ALERT_WEBHOOK:-}" ]; then
  log "(no DFCHAT_ALERT_WEBHOOK set — state change logged but no push)"
  exit 0
fi

# === send webhook ===
host=$(hostname)
ts=$(date '+%F %T %Z')
if [ "$NEW_STATE" = "FAIL" ]; then
  reasons_block=$(printf '  · %s\n' "${reasons[@]}")
  content="🚨 DFCHAT 告警 [$ts]\n服务器: $host\n问题:\n${reasons_block}"
else
  content="✅ DFCHAT 已恢复 [$ts]\n服务器: $host"
fi

# Escape for JSON: backslash + quote + collapse newlines to \n.
json_content=$(printf '%s' "$content" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')

curl -fsS -X POST "$DFCHAT_ALERT_WEBHOOK" \
  -H "Content-Type: application/json" \
  -d "{\"msgtype\":\"text\",\"text\":{\"content\":${json_content}}}" >/dev/null \
  && log "alert posted ($NEW_STATE)" \
  || log "alert post FAILED"
