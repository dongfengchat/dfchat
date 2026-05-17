# DFCHAT — server02 (media edge node)

Pure media: RTMP ingest + HLS slice + signed-URL serve. No app data, no DB.

```
OBS ──RTMP──→ live.dfchat.chat:1935 ──→ SRS ─→ HLS files ──→ nginx+njs
                                          │                       │
                                          │ on_publish hook       │ njs HMAC verify
                                          ▼                       ▼
                       https://app.dfchat.chat/api/v1/live/srs-hook/<secret>
                                          │                       │
                                          ▼                       ▼
                                  server01 api               viewer
```

## File layout

| | |
|---|---|
| `docker-compose.yml` | srs + nginx(+njs) + certbot (no db, no minio) |
| `srs.conf` | RTMP/HLS settings; hook URL points at server01 over HTTPS |
| `nginx.conf` | nginx: TLS + njs HLS verify + njs m3u8 rewrite |
| `njs/hls.js` | HMAC verify + m3u8 rewriter mirroring `play_token.go` |
| `bootstrap.sh` | first-time setup (ssh→13494, docker, ufw, dirs) |
| `close-22.sh` | run on server02 after verifying :13494 works |
| `issue-cert.sh` | one-shot certbot for `live.dfchat.chat` |
| `deploy.sh` | from dev box — rsync + compose up |
| `server01_cutover.md` | how to flip server01 over once this edge is live |

## First-time install order

1. **DNS** (manual, from your DNS dashboard):
   ```
   live.dfchat.chat  A  45.119.4.109  TTL 300
   ```
   Wait for `dig +short live.dfchat.chat` to return that IP from your dev box.

2. **Bootstrap server02** (one-shot, idempotent):
   ```bash
   # From dev box, while SSH is still on :22
   sshpass -p 'Of(dVIWJiRPVCW8h$cZ' scp -P 22 \
     deploy/server02/bootstrap.sh root@45.119.4.109:/tmp/
   sshpass -p 'Of(dVIWJiRPVCW8h$cZ' ssh -p 22 root@45.119.4.109 \
     'bash /tmp/bootstrap.sh'
   ```
   bootstrap.sh ADDS :13494 alongside :22 — does not yet close :22.

3. **Verify new SSH port works, then close :22**:
   ```bash
   sshpass -p 'Of(dVIWJiRPVCW8h$cZ' ssh -p 13494 root@45.119.4.109 'echo ok'
   # If OK:
   sshpass -p 'Of(dVIWJiRPVCW8h$cZ' ssh -p 13494 root@45.119.4.109 \
     'bash /opt/dfchat-live/deploy/close-22.sh'
   ```

4. **Add server02 secrets to .env.local** (dev box):
   ```bash
   cat >> /Users/york/Documents/DFCHAT/.env.local <<'EOF'
   export DFCHAT02_HOST=45.119.4.109
   export DFCHAT02_PORT=13494
   export DFCHAT02_USER=root
   export DFCHAT02_PASSWORD='Of(dVIWJiRPVCW8h$cZ'
   EOF
   ```

5. **First deploy** (rsync configs, start srs + nginx):
   ```bash
   source .env.local
   bash deploy/server02/deploy.sh
   ```

6. **Issue TLS cert** (DNS must resolve correctly first):
   ```bash
   sshpass -p "$DFCHAT02_PASSWORD" ssh -p 13494 root@45.119.4.109 \
     'bash /opt/dfchat-live/deploy/issue-cert.sh your-email@example.com'
   ```

7. **Smoke test the edge in isolation** (server01 still serves HLS too):
   ```bash
   curl -sI https://live.dfchat.chat/healthz                  # 200 ok
   curl -sI https://live.dfchat.chat/hls/nonexistent.m3u8     # 401 (no token)
   ```

8. **Server01 cutover**: see `server01_cutover.md`.

## Re-deploy after config changes

```bash
source .env.local
bash deploy/server02/deploy.sh
```

That's it — idempotent.

## Operational notes

- **Cert renewal** happens automatically in the `certbot` sidecar (12 h loop).
  After a renewal, openresty re-reads the cert files on next worker
  recycle. Force a reload with:
  ```bash
  ssh -p 13494 root@45.119.4.109 \
    'docker compose -f /opt/dfchat-live/deploy/docker-compose.yml exec nginx openresty -s reload'
  ```
- **HLS data lives at** `/opt/dfchat-live/data/srs/live/<key>.m3u8` + ts.
  The bg sweeper in docker-compose.yml drops orphan ts > 1 h old.
- **Logs**: `docker logs deploy-srs-1`, `docker logs deploy-nginx-1`.
- **Clock**: both server01 and server02 MUST be NTP-synced (default on
  Ubuntu 24.04). HMAC verification has a ±30 s tolerance window.
- **NEVER expose** server02's SRS HTTP API (:1985) publicly — bound to
  127.0.0.1 in docker-compose.yml. Anyone reaching it can introspect
  every active stream.
