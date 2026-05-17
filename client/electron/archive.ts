// Encrypted local message archive — client-side primary store.
//
// Design contract (the user calls it "30-day server authority horizon"):
//   - The server retains messages for 30 days, after which it can no
//     longer recall / edit / delete them.
//   - The client keeps a permanent local copy in this SQLite DB.
//   - Message content (the part with actual chat text or file metadata)
//     is encrypted at rest with AES-256-GCM. The key is wrapped via
//     Electron's safeStorage (Keychain on macOS, DPAPI on Windows,
//     libsecret on Linux), so a stolen userData folder alone is not
//     enough to read messages — an attacker also needs the OS account
//     credential / Keychain access.
//   - Indexable metadata (conversation id, sender id, seq, created_at)
//     stays in plaintext so we can paginate, sort, and search without
//     having to decrypt every row.
//
// This file runs in the Electron MAIN process. The renderer reaches
// it through IPC handlers wired in main.ts and exposed in preload.ts
// as window.dfchatArchive.
import { app, safeStorage } from 'electron';
import Database from 'better-sqlite3';
import type { Database as DBType, Statement } from 'better-sqlite3';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

const KEY_FILE = 'dfchat-archive.key';
const DB_FILE = 'dfchat-archive.db';
// AES-GCM nonce length per NIST SP 800-38D recommendation.
const IV_LEN = 12;

let db: DBType | null = null;
let dataKey: Buffer | null = null;
// Prepared statements — built once after open(), reused per call to
// avoid the SQL-parse overhead on hot paths (every chat.recv).
let stmtUpsert: Statement | null = null;
let stmtQueryByConv: Statement | null = null;
let stmtMarkRecalled: Statement | null = null;
let stmtRemove: Statement | null = null;
let stmtMaxSeq: Statement | null = null;
let stmtStats: Statement | null = null;
let stmtAllForExport: Statement | null = null;

// ---------- key wrap / unwrap -----------------------------------------

// loadOrCreateDataKey returns the in-process AES-256 master key. On
// first run we generate a fresh 32 random bytes, ask the OS keychain
// (via safeStorage) to seal it, and write the sealed blob to userData.
// Subsequent launches just unseal the existing blob.
//
// If the OS doesn't support encryption (rare — uninitialised Linux
// keyring), we fall back to writing the key in plaintext. This is
// noted in the log; we'd rather have a working archive than refuse
// to start.
function loadOrCreateDataKey(): Buffer {
  const keyPath = path.join(app.getPath('userData'), KEY_FILE);
  if (fs.existsSync(keyPath)) {
    const raw = fs.readFileSync(keyPath);
    if (safeStorage.isEncryptionAvailable() && raw.length > 0 && raw[0] !== 0x7B /* '{' marker for plaintext json */) {
      try {
        const unwrapped = safeStorage.decryptString(raw);
        return Buffer.from(unwrapped, 'base64');
      } catch {
        // fall through to plaintext-fallback read attempt
      }
    }
    try {
      const obj = JSON.parse(raw.toString('utf8'));
      if (typeof obj.k === 'string') return Buffer.from(obj.k, 'base64');
    } catch { /* generate fresh below */ }
  }
  const fresh = crypto.randomBytes(32);
  if (safeStorage.isEncryptionAvailable()) {
    fs.writeFileSync(keyPath, safeStorage.encryptString(fresh.toString('base64')));
  } else {
    // Plaintext fallback. Marked with a JSON wrapper so we can
    // distinguish it from a safeStorage blob on next launch.
    fs.writeFileSync(keyPath, JSON.stringify({ k: fresh.toString('base64'), _warning: 'plaintext_fallback' }));
  }
  return fresh;
}

function encryptContent(plain: string): { iv: Buffer; tag: Buffer; cipher: Buffer } {
  if (!dataKey) throw new Error('archive not opened');
  const iv = crypto.randomBytes(IV_LEN);
  const c = crypto.createCipheriv('aes-256-gcm', dataKey, iv);
  const cipher = Buffer.concat([c.update(plain, 'utf8'), c.final()]);
  const tag = c.getAuthTag();
  return { iv, tag, cipher };
}

function decryptContent(iv: Buffer, tag: Buffer, cipher: Buffer): string {
  if (!dataKey) throw new Error('archive not opened');
  const d = crypto.createDecipheriv('aes-256-gcm', dataKey, iv);
  d.setAuthTag(tag);
  const plain = Buffer.concat([d.update(cipher), d.final()]);
  return plain.toString('utf8');
}

// ---------- schema + open ---------------------------------------------

export interface ArchivedMessage {
  id: string;
  conversationId: string;
  senderId: string;
  type: string;
  content: unknown; // JSON object — the raw message body, decrypted on read
  seq: number;
  mentions?: string[];
  replyTo?: string;
  isRecalled: boolean;
  editedAt?: string;
  editCount?: number;
  createdAt: string;
}

const SCHEMA = `
CREATE TABLE IF NOT EXISTS messages_archive (
  id              TEXT    PRIMARY KEY,
  conversation_id TEXT    NOT NULL,
  sender_id       TEXT    NOT NULL,
  type            TEXT    NOT NULL,
  content_iv      BLOB    NOT NULL,
  content_tag     BLOB    NOT NULL,
  content_enc     BLOB    NOT NULL,
  seq             INTEGER NOT NULL,
  mentions_json   TEXT,
  reply_to        TEXT,
  is_recalled     INTEGER NOT NULL DEFAULT 0,
  edited_at       TEXT,
  edit_count      INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT    NOT NULL,
  archived_at     TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_archive_conv_seq ON messages_archive(conversation_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_archive_created  ON messages_archive(created_at DESC);
`;

export function open(): void {
  if (db) return;
  dataKey = loadOrCreateDataKey();
  const dbPath = path.join(app.getPath('userData'), DB_FILE);
  db = new Database(dbPath);
  db.pragma('journal_mode = WAL');           // crash-safe + concurrent reads
  db.pragma('synchronous = NORMAL');          // WAL + NORMAL is the typical safe-fast combo
  db.pragma('foreign_keys = ON');
  db.exec(SCHEMA);

  stmtUpsert = db.prepare(`
    INSERT INTO messages_archive
      (id, conversation_id, sender_id, type,
       content_iv, content_tag, content_enc,
       seq, mentions_json, reply_to,
       is_recalled, edited_at, edit_count, created_at)
    VALUES
      (@id, @conversation_id, @sender_id, @type,
       @content_iv, @content_tag, @content_enc,
       @seq, @mentions_json, @reply_to,
       @is_recalled, @edited_at, @edit_count, @created_at)
    ON CONFLICT(id) DO UPDATE SET
      content_iv  = excluded.content_iv,
      content_tag = excluded.content_tag,
      content_enc = excluded.content_enc,
      mentions_json = excluded.mentions_json,
      reply_to    = excluded.reply_to,
      is_recalled = excluded.is_recalled,
      edited_at   = excluded.edited_at,
      edit_count  = excluded.edit_count
  `);

  stmtQueryByConv = db.prepare(`
    SELECT id, conversation_id, sender_id, type,
           content_iv, content_tag, content_enc,
           seq, mentions_json, reply_to,
           is_recalled, edited_at, edit_count, created_at
      FROM messages_archive
     WHERE conversation_id = @conv_id
       AND (@before_seq IS NULL OR seq < @before_seq)
     ORDER BY seq DESC
     LIMIT @limit
  `);

  stmtMarkRecalled = db.prepare(`
    UPDATE messages_archive
       SET is_recalled = 1,
           content_iv  = @content_iv,
           content_tag = @content_tag,
           content_enc = @content_enc
     WHERE id = @id
  `);

  stmtRemove = db.prepare(`DELETE FROM messages_archive WHERE id = @id`);

  stmtMaxSeq = db.prepare(`
    SELECT COALESCE(MAX(seq), 0) AS max_seq
      FROM messages_archive
     WHERE conversation_id = @conv_id
  `);

  stmtStats = db.prepare(`
    SELECT COUNT(*) AS rows,
           MIN(created_at) AS earliest,
           MAX(created_at) AS latest
      FROM messages_archive
  `);

  stmtAllForExport = db.prepare(`
    SELECT id, conversation_id, sender_id, type,
           content_iv, content_tag, content_enc,
           seq, mentions_json, reply_to,
           is_recalled, edited_at, edit_count, created_at
      FROM messages_archive
     ORDER BY conversation_id, seq ASC
  `);
}

export function close(): void {
  if (db) {
    try { db.close(); } catch { /* ignore */ }
    db = null;
  }
  // Zero the key buffer so it isn't recoverable from a memory dump.
  if (dataKey) { dataKey.fill(0); dataKey = null; }
}

// ---------- public CRUD -----------------------------------------------

// appendMessage upserts a row. Encrypts the content body before write.
// Returns nothing; the renderer already has the message object in
// memory — the archive is a write-through cache.
export function appendMessage(msg: ArchivedMessage): void {
  if (!db || !stmtUpsert) throw new Error('archive not opened');
  const plainJson = JSON.stringify(msg.content ?? {});
  const { iv, tag, cipher } = encryptContent(plainJson);
  stmtUpsert.run({
    id: msg.id,
    conversation_id: msg.conversationId,
    sender_id: msg.senderId,
    type: msg.type,
    content_iv: iv,
    content_tag: tag,
    content_enc: cipher,
    seq: msg.seq,
    mentions_json: msg.mentions && msg.mentions.length > 0 ? JSON.stringify(msg.mentions) : null,
    reply_to: msg.replyTo ?? null,
    is_recalled: msg.isRecalled ? 1 : 0,
    edited_at: msg.editedAt ?? null,
    edit_count: msg.editCount ?? 0,
    created_at: msg.createdAt,
  });
}

// markRecalled overwrites the encrypted content with {} (matching the
// server-side redaction) and flips the flag. Other metadata stays so
// the row keeps its place in the seq order.
export function markRecalled(messageId: string): void {
  if (!db || !stmtMarkRecalled) throw new Error('archive not opened');
  const { iv, tag, cipher } = encryptContent('{}');
  stmtMarkRecalled.run({
    id: messageId,
    content_iv: iv,
    content_tag: tag,
    content_enc: cipher,
  });
}

// remove drops the row entirely. Used by the renderer when the user
// chose "delete locally" — the server-driven chat.delete event is
// handled separately and only mirrors there if the message is still
// within the 30-day authority window (see tamper-rejection logic).
export function remove(messageId: string): void {
  if (!db || !stmtRemove) throw new Error('archive not opened');
  stmtRemove.run({ id: messageId });
}

// queryByConv returns up to `limit` messages, newest first, optionally
// before a given seq for pagination. Decrypted before return.
export function queryByConv(convId: string, limit: number, beforeSeq?: number | null): ArchivedMessage[] {
  if (!db || !stmtQueryByConv) throw new Error('archive not opened');
  const rows = stmtQueryByConv.all({
    conv_id: convId,
    limit: Math.max(1, Math.min(500, limit | 0)),
    before_seq: beforeSeq ?? null,
  }) as Array<{
    id: string;
    conversation_id: string;
    sender_id: string;
    type: string;
    content_iv: Buffer;
    content_tag: Buffer;
    content_enc: Buffer;
    seq: number;
    mentions_json: string | null;
    reply_to: string | null;
    is_recalled: number;
    edited_at: string | null;
    edit_count: number;
    created_at: string;
  }>;
  return rows.map((r): ArchivedMessage => ({
    id: r.id,
    conversationId: r.conversation_id,
    senderId: r.sender_id,
    type: r.type,
    content: JSON.parse(decryptContent(r.content_iv, r.content_tag, r.content_enc) || '{}'),
    seq: r.seq,
    mentions: r.mentions_json ? JSON.parse(r.mentions_json) as string[] : undefined,
    replyTo: r.reply_to ?? undefined,
    isRecalled: r.is_recalled !== 0,
    editedAt: r.edited_at ?? undefined,
    editCount: r.edit_count || undefined,
    createdAt: r.created_at,
  }));
}

// maxSeq returns the highest seq stored locally for a conversation, or
// 0 if we have nothing. Used by the sync algorithm to ask the server
// for "anything newer than X" instead of refetching everything.
export function maxSeq(convId: string): number {
  if (!db || !stmtMaxSeq) throw new Error('archive not opened');
  const row = stmtMaxSeq.get({ conv_id: convId }) as { max_seq: number };
  return row?.max_seq ?? 0;
}

export interface ArchiveStats {
  rows: number;
  earliestCreatedAt: string | null;
  latestCreatedAt: string | null;
  dbBytes: number;
}

export function stats(): ArchiveStats {
  if (!db || !stmtStats) throw new Error('archive not opened');
  const r = stmtStats.get() as { rows: number; earliest: string | null; latest: string | null };
  let dbBytes = 0;
  try {
    const dbPath = path.join(app.getPath('userData'), DB_FILE);
    dbBytes = fs.statSync(dbPath).size;
  } catch { /* ignore */ }
  return {
    rows: r?.rows ?? 0,
    earliestCreatedAt: r?.earliest ?? null,
    latestCreatedAt: r?.latest ?? null,
    dbBytes,
  };
}

// exportAll writes every row to the given path as a JSON file. The
// exported file is **plaintext** by design — the point of export is
// to give the user their data in a portable form. They're responsible
// for protecting the resulting file (e.g. saving it on an encrypted
// drive). Returns the number of rows written.
export function exportAll(filePath: string): number {
  if (!db || !stmtAllForExport) throw new Error('archive not opened');
  const rows = stmtAllForExport.all() as Array<{
    id: string;
    conversation_id: string;
    sender_id: string;
    type: string;
    content_iv: Buffer;
    content_tag: Buffer;
    content_enc: Buffer;
    seq: number;
    mentions_json: string | null;
    reply_to: string | null;
    is_recalled: number;
    edited_at: string | null;
    edit_count: number;
    created_at: string;
  }>;
  const out = rows.map((r) => ({
    id: r.id,
    conversationId: r.conversation_id,
    senderId: r.sender_id,
    type: r.type,
    content: JSON.parse(decryptContent(r.content_iv, r.content_tag, r.content_enc) || '{}'),
    seq: r.seq,
    mentions: r.mentions_json ? JSON.parse(r.mentions_json) : undefined,
    replyTo: r.reply_to ?? undefined,
    isRecalled: r.is_recalled !== 0,
    editedAt: r.edited_at ?? undefined,
    editCount: r.edit_count || undefined,
    createdAt: r.created_at,
  }));
  fs.writeFileSync(filePath, JSON.stringify({
    schema: 1,
    exportedAt: new Date().toISOString(),
    count: out.length,
    messages: out,
  }, null, 2));
  return out.length;
}

// importMessages takes the exported JSON shape and writes each message
// into the local archive. ON CONFLICT(id) updates rather than insert,
// so it's safe to run repeatedly or to import a partial export. Returns
// the number of rows touched. Used by the "import from previous device"
// migration UX.
export function importMessages(filePath: string): number {
  if (!db) throw new Error('archive not opened');
  const raw = fs.readFileSync(filePath, 'utf8');
  const parsed = JSON.parse(raw) as { messages?: ArchivedMessage[] };
  if (!Array.isArray(parsed.messages)) return 0;
  let n = 0;
  // Wrap in a transaction — bulk imports are commonly 10k+ rows and
  // an untransacted run is ~100x slower.
  const tx = db.transaction((msgs: ArchivedMessage[]) => {
    for (const m of msgs) {
      appendMessage(m);
      n++;
    }
  });
  tx(parsed.messages);
  return n;
}
