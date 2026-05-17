# Server01 cutover — switching from co-located SRS to server02 edge

After server02 is up and `https://live.dfchat.chat/healthz` returns 200,
server01 has three things to change. **Do them in order.** Each step is
reversible by reverting the file change and rerunning `deploy/deploy.sh`.

## 0. Snapshot the current good state

```bash
cd /Users/york/Documents/DFCHAT
git status                    # should be clean
git log -1 --oneline          # remember this SHA — rollback target
```

If anything breaks mid-cutover: `git checkout <SHA>` + `bash deploy/deploy.sh`.

## 1. server01 nginx — drop /hls block, whitelist server02 IP for SRS hook

Edit `deploy/nginx.conf`:

### 1a. Add server02 IP to the SRS-hook allow list
In the `app.dfchat.chat` server block, find `location /api/v1/live/srs-hook/`
and add **before** the existing `allow` lines:

```nginx
allow 45.119.4.109;             # server02 edge — HLS / SRS
```

### 1b. Delete the entire HLS block from `dfchat.chat` server
Remove (in `dfchat.chat` server block):
- `location ~ ^/hls/(?<stream>[^/]+?)\.m3u8$ { ... }`
- `location ~ ^/hls/(?<segment>[^/]+?)\.ts$ { ... }`
- `location = /_play_auth { ... }`
- `location /flv/ { ... }`         ← also dead (SRS leaving)

Keep `/download/` and the SPA `location /` exactly as is.

### 1c. Delete the `dfchat_srs` upstream
At the top of `http {}`, remove:
```nginx
upstream dfchat_srs   { server srs:8080; }
```

## 2. server01 docker-compose — remove the SRS service entirely

Edit `deploy/docker-compose.prod.yml`:

- Delete the entire `srs:` service block (including its `volumes`, `ports`, etc.)
- Remove `srs` from any `depends_on:` list (none currently depend on it, but double-check)

## 3. server01 .env — point the API at the new RTMP/HLS host

The api signs RTMP push URLs and HLS playback URLs using these envs.
After cutover both must point at server02's host.

In `deploy/.env.prod` (and the live `/opt/dfchat/.env` on server01) change:

```diff
- LIVE_RTMP_URL=rtmp://dfchat.chat:1935/live
+ LIVE_RTMP_URL=rtmp://live.dfchat.chat:1935/live

- LIVE_HLS_URL=https://dfchat.chat/hls
+ LIVE_HLS_URL=https://live.dfchat.chat/hls
```

`LIVE_SRS_SECRET` stays the same value (that's how the HMAC verifies on both ends).

`SRS_API_BASE_URL` (`http://srs:1985/api/v1/summaries`) becomes dead.
The api uses it for viewer-count reconcile; it'll just log connection-refused
warnings until someone wires it to server02's HTTP API. **Acceptable for now**
— viewer counts come from the realtime WS layer too. Cleanup ticket:
"reconnect SRS_API_BASE_URL to server02 internal endpoint, or remove the
reconcile loop entirely."

## 4. Deploy + verify

```bash
source .env.local
bash deploy/deploy.sh

# Confirm SRS container is gone:
sshpass -p "$DFCHAT_PASSWORD" ssh -p 13493 root@198.44.238.9 \
  'docker compose -f /opt/dfchat/deploy/docker-compose.prod.yml ps' | grep srs
# (expect no rows)

# Confirm api is healthy:
curl -sI https://app.dfchat.chat/healthz

# Confirm /hls/ on apex now 404s (we removed the block):
curl -sI https://dfchat.chat/hls/foo.m3u8
# (expect 404, not 500)
```

## 5. End-to-end smoke test

1. Log into the desktop client.
2. Studio → "新建直播间" → get RTMP URL + key. URL should now start with
   `rtmp://live.dfchat.chat:1935/live`.
3. Push from OBS to that URL+key. Within 5 s server01 api should receive
   the on_publish hook via app.dfchat.chat (check `docker logs deploy-api-1`
   for the new SRS-hook line; source IP will be 45.119.4.109).
4. From a second client account, open the room from 广场. Playback URL
   should be `https://live.dfchat.chat/hls/<key>.m3u8?token=...&exp=...`
   and start playing within 5-10 s.
5. Send a 弹幕 — should appear immediately (弹幕 still goes through
   server01 WS, unchanged).
6. Toggle 弹幕设置 → 慢速 5s — should still work.
7. Owner clicks Stop → playback ends, viewer player drops to "已结束".

If any step fails, see the rollback note in step 0.

## 6. Watch logs for 24 h

The most likely surprises after cutover:
- SRS hook IP mismatch (the whitelist line in 1a) — symptoms: nginx logs
  show 403 on /api/v1/live/srs-hook/. Fix: confirm 45.119.4.109 is the
  actual outbound IP server02 uses (sometimes a cloud has a separate
  NAT IP).
- HLS 401s on the client — usually a clock skew between server01 +
  server02 beyond the 30 s tolerance. Both run NTP by default; if not,
  `apt install -y systemd-timesyncd && timedatectl set-ntp true` on
  each.
- Token TTL still 1 h on the server, so if you restart api during
  active playback, existing URLs continue to work until they expire —
  no extra coordination needed.
