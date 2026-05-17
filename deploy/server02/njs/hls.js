// DFCHAT — edge HLS signed-URL verifier + m3u8 rewriter (njs).
//
// Verifies the same HMAC scheme as server01's signPlayToken /
// verifyPlayToken in server/internal/live/play_token.go. Single source
// of truth:
//
//   token = hex( HMAC-SHA256( LIVE_SRS_SECRET, "<stream_key>|<exp_unix>" ) )
//   valid = (token matches, constant-time) AND (now <= exp + 30 s skew)
//
// Why duplicate the Go logic on this host:
// per-segment auth_request roundtrips to server01 would burn its
// (expensive) outbound bandwidth and add 50-100 ms to every .ts fetch.
// Local verify keeps server02 self-sufficient. Go side and njs side
// MUST stay byte-identical — change one, change the other. The pairity
// fixture in tests/hmac_parity.{go,js} catches drift in CI.
//
// HMAC parity verified against Go's crypto/hmac on this fixture:
//   secret = "this-is-the-shared-secret-32chars-long-ok"
//   msg    = "abc123xyz|1778950000"
//   hex    = 2ef31f2e5ffcdd0365ed0bc544314bd9ce7d5017d7c68e3c89435906ad49402d
//
// njs runs synchronously inside nginx workers (single-threaded per
// worker, event-driven across workers). Crypto is sync via openssl —
// safe to call from request handlers without blocking other connections.

import cr from "crypto";
import fs from "fs";

// Secret is injected from nginx config via env. Read once per worker.
// If unset, every verify will fail safely — but log loudly so operators
// notice immediately rather than spending hours chasing 401s.
const SECRET = process.env.LIVE_SRS_SECRET || "";
if (SECRET.length < 16) {
    // Print to stderr so the deploy script can detect the misconfiguration.
    // Note: njs throwing here would just abort the request — that's even
    // worse for triage. We let requests fail with a clear 401 + log line.
    ngx && ngx.log && ngx.log(ngx.ERR,
        "hls.js: LIVE_SRS_SECRET unset or too short — every play token will fail");
}

// HLS playback URL TTL tolerance. Same value as the Go skew constant
// (playTokenSkew = 30 * time.Second) in play_token.go.
const SKEW_SECONDS = 30;

// Constant-time hex string compare. Avoids leaking signature shape via
// timing — see crypto.timingSafeEqual in Node, replicated here because
// njs's crypto module doesn't expose it as of 0.8.x.
function ctEqHex(a, b) {
    if (a.length !== b.length) return false;
    let diff = 0;
    for (let i = 0; i < a.length; i++) {
        diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
    }
    return diff === 0;
}

// Verify the request's ?token=&exp= against the given stream key.
// Returns true on pass, calls r.return(401) and returns false on fail.
function verify(r, stream) {
    if (!stream) {
        r.warn("hls verify: empty stream");
        r.return(401);
        return false;
    }
    const token = r.args.token || "";
    const expStr = r.args.exp || "";
    if (!token || !expStr) {
        r.warn(`hls verify: missing token/exp stream=${stream}`);
        r.return(401);
        return false;
    }
    const exp = parseInt(expStr, 10);
    if (!Number.isFinite(exp)) {
        r.warn(`hls verify: malformed exp=${expStr}`);
        r.return(401);
        return false;
    }
    // Signature compare first (constant-time), then expiry — same order
    // as the Go verifier. Avoids timing side-channel that leaks
    // "good-shape but expired" vs "bad signature".
    const want = cr.createHmac("sha256", SECRET)
        .update(stream + "|" + exp.toString())
        .digest("hex");
    if (!ctEqHex(want, token)) {
        r.warn(`hls verify: bad signature stream=${stream}`);
        r.return(401);
        return false;
    }
    if (exp + SKEW_SECONDS < Math.floor(Date.now() / 1000)) {
        r.warn(`hls verify: expired stream=${stream} exp=${exp}`);
        r.return(401);
        return false;
    }
    return true;
}

// Entry point for /hls/<stream>.m3u8 requests. Verify the URL, read
// the m3u8 from disk (SRS writes here), append ?token=&exp= to every
// segment line, and return the rewritten playlist.
//
// Why we rewrite in proc rather than letting the HLS player reuse
// the m3u8's query string: HLS players resolve segment URLs RELATIVE
// to the playlist URL but DO NOT inherit its query parameters. Every
// segment line must carry its own token. server01 used to do this in
// Go (appendTokenToSegments); now we do it at the edge.
function m3u8(r) {
    // Stream key was captured by nginx as $stream and passed here.
    const stream = r.variables.stream;
    if (!verify(r, stream)) return;

    const path = "/var/www/hls/live/" + stream + ".m3u8";
    let body;
    try {
        body = fs.readFileSync(path, "utf8");
    } catch (e) {
        // ENOENT means SRS hasn't sliced any segments yet (stream just
        // started or never came up). Client treats 404 as "offline".
        r.warn(`m3u8 not found: ${path} (${e.message})`);
        r.return(404);
        return;
    }
    const qs = "token=" + r.args.token + "&exp=" + r.args.exp;
    const out = rewritePlaylist(body, qs);
    r.headersOut["Content-Type"] = "application/vnd.apple.mpegurl";
    r.headersOut["Cache-Control"] = "no-cache";
    r.headersOut["Access-Control-Allow-Origin"] = "*";
    r.return(200, out);
}

// Pure-string playlist rewriter. Preserves directives (#-prefixed) and
// blanks; appends ?qs (or &qs if a ? is already present) to media
// segment lines. Preserves \r\n vs \n line endings as authored by SRS.
function rewritePlaylist(body, qs) {
    const lines = body.split("\n");
    for (let i = 0; i < lines.length; i++) {
        const raw = lines[i];
        // Strip a trailing \r for inspection but remember to put it back.
        const hasCR = raw.length > 0 && raw.charCodeAt(raw.length - 1) === 13;
        const trimmed = hasCR ? raw.substring(0, raw.length - 1) : raw;
        if (trimmed === "" || trimmed.charAt(0) === "#") continue; // directive or blank
        const sep = trimmed.indexOf("?") >= 0 ? "&" : "?";
        lines[i] = trimmed + sep + qs + (hasCR ? "\r" : "");
    }
    return lines.join("\n");
}

// Entry point for /hls/<stream>-<N>.ts. njs verifies the BASE stream key
// (the same one HMAC was signed with — Go signs the room's stream_key,
// not per-segment names). Returns true on pass so nginx then serves the
// file from disk via internalRedirect. On fail returns false and the
// 401 has already been sent.
function ts(r) {
    const segfile = r.variables.segfile;  // e.g. "abc123-7"
    // Strip trailing "-<digits>" to recover base stream key. Mirrors
    // server01's playAuth's basename-strip on its side.
    const m = segfile.match(/^(.+?)-\d+$/);
    const base = m ? m[1] : segfile;
    if (!verify(r, base)) return;
    // Verified — hand off to the internal file location to serve via
    // sendfile() for max throughput.
    r.internalRedirect("/_hls_file/" + segfile + ".ts");
}

export default { m3u8, ts };
