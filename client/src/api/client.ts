import axios, { AxiosError, type AxiosRequestConfig } from 'axios';
import type { ApiError, Channel, ChatMessage, Friend, Group, GroupMember, LiveRoom, LiveRoomDetail, LoginResponse, Pin, ReactionCount, User } from '@/types';

const API_BASE = import.meta.env.VITE_API_BASE ?? 'http://localhost:8080';

export const api = axios.create({
  baseURL: API_BASE,
  timeout: 15000,
});

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('accessToken');
  if (token) config.headers.Authorization = `Bearer ${token}`;
  return config;
});

// 401 handler: try refresh once, retry the original request, then bounce to
// /login on permanent failure. The refresh dance is intentionally single-
// flight so concurrent 401s share one swap.
let refreshInFlight: Promise<string | null> | null = null;
let sessionBouncePending = false;

async function tryRefresh(): Promise<string | null> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    const refreshToken = localStorage.getItem('refreshToken');
    if (!refreshToken) return null;
    try {
      const res = await axios.post<LoginResponse>(`${API_BASE}/api/v1/auth/refresh`, { refreshToken });
      localStorage.setItem('accessToken', res.data.accessToken);
      localStorage.setItem('refreshToken', res.data.refreshToken);
      return res.data.accessToken;
    } catch {
      return null;
    } finally {
      // Allow next refresh on next 401.
      setTimeout(() => (refreshInFlight = null), 0);
    }
  })();
  return refreshInFlight;
}

function bounceToLogin(reason = 'expired') {
  if (sessionBouncePending) return;
  sessionBouncePending = true;
  try {
    localStorage.removeItem('accessToken');
    localStorage.removeItem('refreshToken');
    localStorage.removeItem('dfchat.lastSeq');
    localStorage.removeItem('dfchat.lastReadSeq');
  } catch { /* ignore */ }
  setTimeout(() => {
    window.location.hash = `#/login?expired=1`;
    sessionBouncePending = false;
  }, 50);
}

api.interceptors.response.use(
  (res) => res,
  async (err: AxiosError<ApiError>) => {
    const original = err.config as (AxiosRequestConfig & { _retried?: boolean }) | undefined;
    const status = err.response?.status;
    const isAuthCall =
      original?.url?.includes('/auth/login') ||
      original?.url?.includes('/auth/refresh') ||
      original?.url?.includes('/auth/register');
    if (status === 401 && original && !original._retried && !isAuthCall) {
      const newAccess = await tryRefresh();
      if (newAccess) {
        original._retried = true;
        original.headers = { ...(original.headers || {}), Authorization: `Bearer ${newAccess}` };
        return api(original);
      }
      const path = window.location.hash.replace(/^#/, '');
      if (path !== '/login' && path !== '/register') bounceToLogin();
    }
    return Promise.reject(err);
  },
);

function unwrapError(err: unknown): ApiError {
  const ae = err as AxiosError<ApiError>;
  if (ae.response?.data?.message) return ae.response.data;
  return { code: -1, message: ae.message || 'network error' };
}

export async function register(input: {
  username: string;
  email: string;
  password: string;
  nickname?: string;
  accountNo: string;
  selectionToken: string;
  website?: string; // honeypot — always "" for real users
}): Promise<User> {
  try {
    const res = await api.post<{ user: User }>('/api/v1/auth/register', input);
    return res.data.user;
  } catch (e) {
    throw unwrapError(e);
  }
}

export interface AccountNoDraw {
  numbers: string[];
  selectionToken: string;
  refreshesLeft: number;
}

// drawAccountNumbers gets 10 random unclaimed account numbers from the
// current open segment. The server reserves them for 10 minutes — the
// token must be carried through to the eventual register call.
export async function drawAccountNumbers(): Promise<AccountNoDraw> {
  try {
    const res = await api.post<AccountNoDraw>('/api/v1/auth/account-no/draw');
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

// refreshAccountNumbers swaps the current 10 for a fresh batch. Server
// enforces a hard cap of 3 refreshes per draw session.
export async function refreshAccountNumbers(selectionToken: string): Promise<AccountNoDraw> {
  try {
    const res = await api.post<AccountNoDraw>('/api/v1/auth/account-no/refresh', { selectionToken });
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function login(login: string, password: string): Promise<LoginResponse> {
  try {
    const res = await api.post<LoginResponse>('/api/v1/auth/login', { login, password });
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function fetchMe(): Promise<User> {
  try {
    const res = await api.get<{ user: User }>('/api/v1/users/me');
    return res.data.user;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function updateMe(input: {
  nickname?: string;
  bio?: string;
  avatarUrl?: string;
}): Promise<User> {
  try {
    const res = await api.patch<{ user: User }>('/api/v1/users/me', input);
    return res.data.user;
  } catch (e) { throw unwrapError(e); }
}

export interface SessionItem {
  id: string;
  device: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt: string;
  isCurrent: boolean;
}

export async function listSessions(): Promise<SessionItem[]> {
  try {
    const refreshToken = localStorage.getItem('refreshToken') ?? '';
    const res = await api.get<{ sessions: SessionItem[] }>('/api/v1/auth/sessions', {
      headers: { 'X-Refresh-Token': refreshToken },
    });
    return res.data.sessions ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function revokeSession(id: string): Promise<void> {
  try { await api.delete(`/api/v1/auth/sessions/${id}`); } catch (e) { throw unwrapError(e); }
}

export async function logoutServer(): Promise<void> {
  try {
    const refreshToken = localStorage.getItem('refreshToken') ?? '';
    if (!refreshToken) return;
    await api.post('/api/v1/auth/logout', { refreshToken });
  } catch { /* logout is best-effort */ }
}

export async function setConversationPreferences(conversationId: string, muted: boolean): Promise<void> {
  try {
    await api.patch(`/api/v1/conversations/${encodeURIComponent(conversationId)}/preferences`, { muted });
  } catch (e) { throw unwrapError(e); }
}

export async function listFriends(): Promise<Friend[]> {
  try {
    const res = await api.get<{ friends: Friend[] }>('/api/v1/friends');
    return res.data.friends ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export interface FriendRequest {
  userId: string;
  username: string;
  nickname: string;
  avatarUrl?: string;
  createdAt: string;
}

/**
 * Sends a pending friend request. Returns the target user id, and a status
 * which is currently always "pending" (the recipient must accept).
 *
 * Server returns 409 if you're already friends or the request is already
 * outstanding; that surfaces via the thrown ApiError.
 */
export async function addFriend(username: string): Promise<{ targetUserId: string; status: string }> {
  try {
    const res = await api.post<{ targetUserId: string; status: string }>('/api/v1/friends/requests', { username });
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function listFriendRequests(): Promise<{ incoming: FriendRequest[]; outgoing: FriendRequest[] }> {
  try {
    const res = await api.get<{ incoming: FriendRequest[]; outgoing: FriendRequest[] }>('/api/v1/friends/requests');
    return { incoming: res.data.incoming ?? [], outgoing: res.data.outgoing ?? [] };
  } catch (e) { throw unwrapError(e); }
}

export async function acceptFriendRequest(fromUserId: string): Promise<void> {
  try { await api.post(`/api/v1/friends/requests/${fromUserId}/accept`); } catch (e) { throw unwrapError(e); }
}

// Works for both rejecting incoming and cancelling outgoing — server picks.
export async function dropFriendRequest(otherUserId: string): Promise<void> {
  try { await api.delete(`/api/v1/friends/requests/${otherUserId}`); } catch (e) { throw unwrapError(e); }
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  try { await api.post('/api/v1/auth/change-password', { currentPassword, newPassword }); } catch (e) { throw unwrapError(e); }
}

export async function blockUser(userId: string): Promise<void> {
  try { await api.post(`/api/v1/friends/blocked/${userId}`); } catch (e) { throw unwrapError(e); }
}

export async function unblockUser(userId: string): Promise<void> {
  try { await api.delete(`/api/v1/friends/blocked/${userId}`); } catch (e) { throw unwrapError(e); }
}

export async function listBlockedUsers(): Promise<Friend[]> {
  try {
    const res = await api.get<{ blocked: Friend[] }>('/api/v1/friends/blocked');
    return res.data.blocked ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function revokeOtherSessions(): Promise<number> {
  try {
    const refreshToken = localStorage.getItem('refreshToken') ?? '';
    const res = await api.post<{ revoked: number }>('/api/v1/auth/sessions/revoke-others', null, {
      headers: { 'X-Refresh-Token': refreshToken },
    });
    return res.data.revoked;
  } catch (e) { throw unwrapError(e); }
}

export async function deleteMe(password: string): Promise<void> {
  try {
    await api.delete('/api/v1/users/me', { data: { password } });
  } catch (e) { throw unwrapError(e); }
}

// ====== Live streaming ============================================

// listLiveRooms now supports optional Discover filters (q text search +
// category). Empty / undefined params hit the unfiltered endpoint.
export async function listLiveRooms(params?: { q?: string; category?: string; limit?: number }): Promise<{ rooms: LiveRoom[]; hlsBase: string }> {
  try {
    const qs = new URLSearchParams();
    if (params?.q) qs.set('q', params.q);
    if (params?.category) qs.set('category', params.category);
    if (params?.limit) qs.set('limit', String(params.limit));
    const suffix = qs.toString() ? `?${qs.toString()}` : '';
    const res = await api.get<{ rooms: LiveRoom[]; hlsBase: string }>(`/api/v1/live/rooms${suffix}`);
    return { rooms: res.data.rooms ?? [], hlsBase: res.data.hlsBase };
  } catch (e) { throw unwrapError(e); }
}

export async function getLiveRoom(id: string): Promise<LiveRoomDetail> {
  try {
    const res = await api.get<LiveRoomDetail>(`/api/v1/live/rooms/${id}`);
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

export async function createLiveRoom(title: string, category?: string): Promise<LiveRoomDetail> {
  try {
    const res = await api.post<LiveRoomDetail>('/api/v1/live/rooms', { title, category });
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

export async function getLiveRoomOwner(id: string): Promise<LiveRoomDetail> {
  try {
    const res = await api.get<LiveRoomDetail>(`/api/v1/live/rooms/${id}/owner`);
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

export async function listMyLiveRooms(): Promise<LiveRoom[]> {
  try {
    const res = await api.get<{ rooms: LiveRoom[] }>('/api/v1/live/mine');
    return res.data.rooms ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function rotateLiveStreamKey(id: string): Promise<{ streamKey: string; rtmpUrl: string }> {
  try {
    const res = await api.post<{ streamKey: string; rtmpUrl: string }>(`/api/v1/live/rooms/${id}/rotate-key`);
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

export async function updateLiveRoom(id: string, patch: { title?: string; category?: string; coverUrl?: string }): Promise<LiveRoom> {
  try {
    const res = await api.patch<{ room: LiveRoom }>(`/api/v1/live/rooms/${id}`, patch);
    return res.data.room;
  } catch (e) { throw unwrapError(e); }
}

export async function deleteLiveRoom(id: string): Promise<void> {
  try { await api.delete(`/api/v1/live/rooms/${id}`); } catch (e) { throw unwrapError(e); }
}

// stopLiveRoom flips the room into "ended" state without deleting it.
// Used when the host wants to manually end a broadcast — useful if
// OBS crashed silently and SRS never fired the on_unpublish hook,
// leaving the room stuck at status=1. Also rotates the stream key on
// the server so any leaked URL can't be repushed.
export async function stopLiveRoom(id: string): Promise<void> {
  try { await api.post(`/api/v1/live/rooms/${id}/stop`); } catch (e) { throw unwrapError(e); }
}

// updateLiveChatSettings flips slow-mode + subscriber-only on a live
// room. Owner-only. Both fields optional — undefined leaves as-is.
export async function updateLiveChatSettings(
  id: string,
  patch: { slowModeSeconds?: number; chatSubscribersOnly?: boolean },
): Promise<LiveRoom> {
  try {
    const res = await api.patch<{ room: LiveRoom }>(`/api/v1/live/rooms/${id}/chat-settings`, patch);
    return res.data.room;
  } catch (e) { throw unwrapError(e); }
}

// pinLiveDanmaku pins a chosen danmaku as the room's top-of-chat
// highlight. Replaces any previous pin. senderId defaults to the
// caller (owner pinning their own) if omitted server-side.
export async function pinLiveDanmaku(
  id: string,
  body: { text: string; color?: string; senderId?: string },
): Promise<LiveRoom> {
  try {
    const res = await api.post<{ room: LiveRoom }>(`/api/v1/live/rooms/${id}/pin-danmaku`, body);
    return res.data.room;
  } catch (e) { throw unwrapError(e); }
}

export async function unpinLiveDanmaku(id: string): Promise<void> {
  try { await api.delete(`/api/v1/live/rooms/${id}/pin-danmaku`); } catch (e) { throw unwrapError(e); }
}

// Flip a room between test-broadcast (host-only) and public (in discover).
// New rooms default to isTest=true; the owner publishes by passing false.
// === Group polish ===
export async function updateGroup(id: string, patch: {
  name?: string;
  iconUrl?: string;
  description?: string;
  announcement?: string;
  isPublic?: boolean;
}): Promise<Group> {
  try {
    const res = await api.patch<{ group: Group }>(`/api/v1/groups/${id}`, patch);
    return res.data.group;
  } catch (e) { throw unwrapError(e); }
}

export async function leaveGroup(id: string): Promise<void> {
  try { await api.delete(`/api/v1/groups/${id}/leave`); } catch (e) { throw unwrapError(e); }
}

// deleteGroup dissolves a group entirely. Owner-only on the server.
// Cascades to members / channels / convs; historical messages persist
// but become unreachable since nobody is in conversation_members.
export async function deleteGroup(id: string): Promise<void> {
  try { await api.delete(`/api/v1/groups/${id}`); } catch (e) { throw unwrapError(e); }
}

// transferGroupOwner hands ownership to another existing member. The
// caller (current owner) is demoted to admin so they keep enough
// power to leave gracefully or assist the new owner.
export async function transferGroupOwner(id: string, newOwnerUserId: string): Promise<void> {
  try { await api.post(`/api/v1/groups/${id}/transfer`, { userId: newOwnerUserId }); } catch (e) { throw unwrapError(e); }
}

// rotateGroupInviteCode mints a fresh invite_code, invalidating the
// old one. Owner or admin only — useful when a leaked code is being
// abused without wanting to kick anyone.
export async function rotateGroupInviteCode(id: string): Promise<string> {
  try {
    const res = await api.post<{ inviteCode: string }>(`/api/v1/groups/${id}/invite/rotate`);
    return res.data.inviteCode;
  } catch (e) { throw unwrapError(e); }
}

// listGroupMembers now supports an optional case-insensitive substring
// filter on username/nickname — used by the in-group "find member"
// UI without needing a separate endpoint.
export async function searchGroupMembers(groupId: string, q: string): Promise<GroupMember[]> {
  try {
    const res = await api.get<{ members: GroupMember[] }>(
      `/api/v1/groups/${groupId}/members${q ? `?q=${encodeURIComponent(q)}` : ''}`,
    );
    return res.data.members ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function getGroupNotifyMode(id: string): Promise<number> {
  try {
    const res = await api.get<{ mode: number }>(`/api/v1/groups/${id}/notify`);
    return res.data.mode;
  } catch (e) { throw unwrapError(e); }
}

export async function setGroupNotifyMode(id: string, mode: 0 | 1 | 2): Promise<void> {
  try { await api.patch(`/api/v1/groups/${id}/notify`, { mode }); } catch (e) { throw unwrapError(e); }
}

export interface LiveViewer {
  userId: string;
}
export async function listLiveViewers(roomId: string): Promise<{ userIds: string[]; count: number }> {
  try {
    const res = await api.get<{ userIds: string[]; count: number }>(`/api/v1/live/rooms/${roomId}/viewers`);
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

export interface ConvFile {
  id: string;
  senderId: string;
  type: 'image' | 'file';
  name: string;
  url: string;
  size?: number;
  mime?: string;
  thumbnail?: string;
  createdAt: string;
}

export async function listConversationFiles(convId: string): Promise<ConvFile[]> {
  try {
    const res = await api.get<{ files: ConvFile[] }>(`/api/v1/files/by-conversation/${encodeURIComponent(convId)}`);
    return res.data.files ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function setLiveRoomVisibility(id: string, isTest: boolean): Promise<LiveRoom> {
  try {
    const res = await api.patch<{ room: LiveRoom }>(`/api/v1/live/rooms/${id}/visibility`, { isTest });
    return res.data.room;
  } catch (e) { throw unwrapError(e); }
}

export interface LiveRecording {
  id: string;
  roomId: string;
  fileUrl: string;
  duration: number;        // seconds
  sizeBytes: number;
  createdAt: string;
}

export async function listLiveRecordings(roomId: string): Promise<LiveRecording[]> {
  try {
    const res = await api.get<{ recordings: LiveRecording[] }>(`/api/v1/live/rooms/${roomId}/recordings`);
    return res.data.recordings ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function deleteLiveRecording(recordingId: string): Promise<void> {
  try { await api.delete(`/api/v1/live/recordings/${recordingId}`); } catch (e) { throw unwrapError(e); }
}

// === Followers ===
export async function followLiveRoom(roomId: string): Promise<void> {
  try { await api.post(`/api/v1/live/rooms/${roomId}/follow`); } catch (e) { throw unwrapError(e); }
}

export async function unfollowLiveRoom(roomId: string): Promise<void> {
  try { await api.delete(`/api/v1/live/rooms/${roomId}/follow`); } catch (e) { throw unwrapError(e); }
}

export async function getLiveRoomFollowStatus(roomId: string): Promise<{ following: boolean; count: number }> {
  try {
    const res = await api.get<{ following: boolean; count: number }>(`/api/v1/live/rooms/${roomId}/follow`);
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

// === Persisted danmaku history ===
export interface LiveDanmakuApiItem {
  id: string;
  roomId: string;
  senderId: string;
  // Server populates these by LEFT JOIN on users at SELECT time so the
  // chat side-panel can render "<昵称> #<账号>" without a follow-up
  // lookup. Empty when the sender's account has been deleted.
  senderNickname?: string;
  senderAccountNo?: string;
  text: string;
  color?: string;
  ts: string;
}

export async function listLiveDanmaku(roomId: string, limit = 50): Promise<LiveDanmakuApiItem[]> {
  try {
    const res = await api.get<{ danmaku: LiveDanmakuApiItem[] }>(
      `/api/v1/live/rooms/${roomId}/danmaku`,
      { params: { limit } },
    );
    return res.data.danmaku ?? [];
  } catch (e) { throw unwrapError(e); }
}

// === Bans / kicks (owner) ===
export async function banLiveUser(roomId: string, userId: string, isKick: boolean, reason?: string): Promise<void> {
  try {
    await api.post(`/api/v1/live/rooms/${roomId}/bans`, { userId, isKick, reason });
  } catch (e) { throw unwrapError(e); }
}

export async function unbanLiveUser(roomId: string, userId: string): Promise<void> {
  try { await api.delete(`/api/v1/live/rooms/${roomId}/bans/${userId}`); } catch (e) { throw unwrapError(e); }
}

// === Scheduling ===
export async function setLiveRoomSchedule(roomId: string, scheduledAt: string | null): Promise<void> {
  try {
    await api.patch(`/api/v1/live/rooms/${roomId}/schedule`, { scheduledAt });
  } catch (e) { throw unwrapError(e); }
}

export async function listScheduledLiveRooms(): Promise<LiveRoom[]> {
  try {
    const res = await api.get<{ rooms: LiveRoom[] }>('/api/v1/live/scheduled');
    return res.data.rooms ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function listMessages(conversationId: string, limit = 50): Promise<ChatMessage[]> {
  try {
    const res = await api.get<{ messages: ChatMessage[] }>('/api/v1/messages', {
      params: { conversationId, limit },
    });
    return res.data.messages ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function listMessagesAfter(
  conversationId: string,
  afterSeq: number,
  limit = 200,
): Promise<ChatMessage[]> {
  try {
    const res = await api.get<{ messages: ChatMessage[] }>('/api/v1/messages', {
      params: { conversationId, afterSeq, limit },
    });
    return res.data.messages ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function listMessagesAround(
  conversationId: string,
  aroundSeq: number,
  limit = 25,
): Promise<ChatMessage[]> {
  try {
    const res = await api.get<{ messages: ChatMessage[] }>('/api/v1/messages', {
      params: { conversationId, aroundSeq, limit },
    });
    return res.data.messages ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export interface UploadToken {
  fileId: string;
  uploadUrl: string;
  publicUrl: string;
  expiresIn: number;
  storageKey: string;
}

export interface UploadedFile {
  id: string;
  userId: string;
  name: string;
  mime?: string;
  size: number;
  url: string;
  thumbnail?: string;
  createdAt: string;
}

export async function requestUploadToken(input: {
  name: string;
  mime: string;
  size: number;
  kind: 'image' | 'file';
}): Promise<UploadToken> {
  try {
    const res = await api.post<UploadToken>('/api/v1/files/upload-token', input);
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function confirmUpload(fileId: string): Promise<UploadedFile> {
  try {
    const res = await api.post<{ file: UploadedFile }>('/api/v1/files/confirm', { fileId });
    return res.data.file;
  } catch (e) {
    throw unwrapError(e);
  }
}

/** uploadBlob runs the full presigned-PUT + confirm dance. */
export async function uploadBlob(
  blob: Blob,
  name: string,
  kind: 'image' | 'file',
): Promise<UploadedFile> {
  const mime = blob.type || (kind === 'image' ? 'image/png' : 'application/octet-stream');
  const token = await requestUploadToken({ name, mime, size: blob.size, kind });
  const put = await fetch(token.uploadUrl, {
    method: 'PUT',
    body: blob,
    headers: { 'Content-Type': mime },
  });
  if (!put.ok) {
    throw { code: -1, message: `upload failed: HTTP ${put.status}` };
  }
  return confirmUpload(token.fileId);
}

export interface ConvSummary {
  id: string;
  type: number;
  headSeq: number;
  lastMessageAt?: string;
  muted?: boolean;
}

export async function syncConversations(): Promise<ConvSummary[]> {
  try {
    const res = await api.get<{ conversations: ConvSummary[] }>('/api/v1/sync/conversations');
    return res.data.conversations ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

interface SendBody {
  to?: string;
  groupId?: string;
  channelId?: string;
  type: string;
  content: Record<string, unknown>;
  mentions?: string[];
  replyTo?: string;
}

async function postMessage(body: SendBody): Promise<ChatMessage> {
  try {
    const res = await api.post<{ message: ChatMessage }>('/api/v1/messages', body);
    return res.data.message;
  } catch (e) {
    throw unwrapError(e);
  }
}

interface TextOpts {
  mentions?: string[];
  replyTo?: string;
}

export function sendPrivateMessage(to: string, text: string, opts?: TextOpts): Promise<ChatMessage> {
  return postMessage({ to, type: 'text', content: { text }, mentions: opts?.mentions, replyTo: opts?.replyTo });
}

export function sendChannelMessage(channelId: string, text: string, opts?: TextOpts): Promise<ChatMessage> {
  return postMessage({ channelId, type: 'text', content: { text }, mentions: opts?.mentions, replyTo: opts?.replyTo });
}

export function sendPrivateRich(to: string, type: 'image' | 'file', content: Record<string, unknown>, replyTo?: string) {
  return postMessage({ to, type, content, replyTo });
}

export function sendChannelRich(channelId: string, type: 'image' | 'file', content: Record<string, unknown>, replyTo?: string) {
  return postMessage({ channelId, type, content, replyTo });
}

export async function recallMessage(id: string): Promise<ChatMessage> {
  try {
    const res = await api.post<{ message: ChatMessage }>(`/api/v1/messages/${id}/recall`);
    return res.data.message;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function listChannels(groupId: string): Promise<Channel[]> {
  try {
    const res = await api.get<{ channels: Channel[] }>(`/api/v1/groups/${groupId}/channels`);
    return res.data.channels ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function createChannel(groupId: string, name: string): Promise<Channel> {
  try {
    const res = await api.post<{ channel: Channel }>(`/api/v1/groups/${groupId}/channels`, { name });
    return res.data.channel;
  } catch (e) {
    throw unwrapError(e);
  }
}

// renameChannel updates the display name of a channel. Server gates
// this on owner/admin role in the parent group.
export async function renameChannel(channelId: string, name: string): Promise<Channel> {
  try {
    const res = await api.patch<{ channel: Channel }>(`/api/v1/channels/${channelId}`, { name });
    return res.data.channel;
  } catch (e) { throw unwrapError(e); }
}

// reorderChannels sets the new top-to-bottom order of channels in a
// group. Channels not mentioned keep their existing relative order
// pushed below the supplied list.
export async function reorderChannels(groupId: string, orderedChannelIds: string[]): Promise<void> {
  try {
    await api.patch(`/api/v1/groups/${groupId}/channels/positions`, { order: orderedChannelIds });
  } catch (e) { throw unwrapError(e); }
}

// deleteChannel — owner/admin only. Server refuses to delete the last
// remaining channel in a group (creates a stranded conversation otherwise).
export async function deleteChannel(channelId: string): Promise<void> {
  try { await api.delete(`/api/v1/channels/${channelId}`); } catch (e) { throw unwrapError(e); }
}

// editMessage rewrites the text of an own message within the server's
// 5-minute edit window. Only `type:"text"` messages are editable.
// Returns the updated row with editedAt + editCount populated.
export async function editMessage(messageId: string, text: string): Promise<ChatMessage> {
  try {
    const res = await api.patch<{ message: ChatMessage }>(`/api/v1/messages/${messageId}`, { content: { text } });
    return res.data.message;
  } catch (e) { throw unwrapError(e); }
}

// deleteMessage permanently removes an own message from the server.
// Different from recall: recall keeps a redacted placeholder row for
// seq continuity; delete drops the row entirely. Server enforces a
// 30-day retention window (after which the sweeper has already
// cleaned the row up anyway). Local archive — if any — is unaffected
// by design: this is the "30-day server authority horizon" model.
export async function deleteMessage(messageId: string): Promise<void> {
  try {
    await api.delete(`/api/v1/messages/${messageId}`);
  } catch (e) { throw unwrapError(e); }
}

export interface AdminStats {
  totalUsers: number;
  totalGroups: number;
  messagesToday: number;
  totalMessages: number;
  totalFiles: number;
}

export interface AdminUser {
  id: string;
  accountNo: string;
  username: string;
  email: string;
  nickname: string;
  status: number;
  isAdmin: boolean;
  emailVerified: boolean;
  lastLoginAt?: string;
  lastLoginIp?: string;
  registeredFromIp?: string;
  createdAt: string;
}

export interface AdminSegmentStat {
  segmentNo: number;
  rangeStart: number;
  rangeEnd: number;
  state: string;
  total: number;
  claimed: number;
  locked: number;
  reserved: number;
  free: number;
  openedAt: string;
}

export async function adminStats(): Promise<AdminStats> {
  try {
    const res = await api.get<AdminStats>('/api/v1/admin/stats');
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function adminAccountPoolStats(): Promise<AdminSegmentStat[]> {
  try {
    const res = await api.get<{ segments: AdminSegmentStat[] }>('/api/v1/admin/account-pool');
    return res.data.segments ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function adminListUsers(opts?: { search?: string; limit?: number; offset?: number; ip?: string }): Promise<AdminUser[]> {
  try {
    const res = await api.get<{ users: AdminUser[] }>('/api/v1/admin/users', { params: opts });
    return res.data.users ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

// === Email verification + password reset (public) ===
export async function forgotPassword(email: string): Promise<{ ok: boolean; devLink?: string }> {
  try {
    const res = await api.post<{ ok: boolean; devLink?: string }>('/api/v1/auth/forgot-password', { email });
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

export async function resetPassword(token: string, newPassword: string): Promise<void> {
  try {
    await api.post('/api/v1/auth/reset-password', { token, newPassword });
  } catch (e) { throw unwrapError(e); }
}

export interface LoginLogEntry {
  id: string;
  success: boolean;
  ip: string;
  userAgent: string;
  createdAt: string;
}

export async function recentLogins(): Promise<LoginLogEntry[]> {
  try {
    const res = await api.get<{ logs: LoginLogEntry[] }>('/api/v1/auth/recent-logins');
    return res.data.logs ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function sendVerificationEmail(): Promise<{ ok: boolean; alreadyVerified?: boolean; devLink?: string }> {
  try {
    const res = await api.post<{ ok: boolean; alreadyVerified?: boolean; devLink?: string }>('/api/v1/auth/send-verification');
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

// requestEmailChange queues a change of the registered email. The user
// must re-enter their current password (defence against open-session
// hijack), and the new mailbox must click a confirmation link before the
// swap takes effect. Returns dev-mode link when SMTP isn't configured.
export async function requestEmailChange(newEmail: string, currentPassword: string): Promise<{ ok: boolean; devLink?: string }> {
  try {
    const res = await api.post<{ ok: boolean; devLink?: string }>('/api/v1/auth/request-email-change', { newEmail, currentPassword });
    return res.data;
  } catch (e) { throw unwrapError(e); }
}

export async function adminSetUserStatus(userId: string, status: number): Promise<void> {
  try {
    await api.patch(`/api/v1/admin/users/${userId}/status`, { status });
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function adminUserLogins(userId: string, limit = 50): Promise<LoginLogEntry[]> {
  try {
    const res = await api.get<{ logs: LoginLogEntry[] }>(`/api/v1/admin/users/${userId}/logins`, { params: { limit } });
    return res.data.logs ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function adminForceLogoutUser(userId: string): Promise<{ revoked: number }> {
  try {
    const res = await api.post<{ revoked: number }>(`/api/v1/admin/users/${userId}/force-logout`);
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

export interface AdminPremiumNumber {
  accountNo: string;
  segmentNo: number;
  claimed: boolean;
  claimedBy?: string;
  ownerName?: string;
}

export async function adminListPremiumNumbers(segment?: number, limit = 200): Promise<AdminPremiumNumber[]> {
  try {
    const res = await api.get<{ numbers: AdminPremiumNumber[] }>('/api/v1/admin/premium-numbers', { params: { segment, limit } });
    return res.data.numbers ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function adminGrantPremiumNumber(premiumNo: string, userAccountNo: string): Promise<{ newAccountNo: string; previousAccountNo: string }> {
  try {
    const res = await api.post<{ ok: boolean; newAccountNo: string; previousAccountNo: string }>(
      `/api/v1/admin/premium-numbers/${premiumNo}/grant`,
      { userAccountNo },
    );
    return res.data;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function adminReleasePremiumNumber(premiumNo: string): Promise<void> {
  try {
    await api.post(`/api/v1/admin/premium-numbers/${premiumNo}/release`);
  } catch (e) {
    throw unwrapError(e);
  }
}

export interface AdminLiveRoom {
  id: string;
  ownerId: string;
  ownerName: string;
  title: string;
  category?: string;
  status: number;       // 0 idle, 1 live, 2 ended, 3 banned
  isTest: boolean;
  viewerCount: number;
  totalViews: number;
  startedAt?: string;
  createdAt: string;
}

export async function adminListLiveRooms(status?: 'live' | 'ended' | 'banned'): Promise<AdminLiveRoom[]> {
  try {
    const res = await api.get<{ rooms: AdminLiveRoom[] }>('/api/v1/admin/live/rooms', { params: status ? { status } : undefined });
    return res.data.rooms ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function adminForceEndLive(id: string): Promise<void> {
  try { await api.post(`/api/v1/admin/live/rooms/${id}/force-end`); } catch (e) { throw unwrapError(e); }
}

export async function adminBanLiveRoom(id: string, banned: boolean, reason?: string): Promise<void> {
  try { await api.patch(`/api/v1/admin/live/rooms/${id}/ban`, { banned, reason }); } catch (e) { throw unwrapError(e); }
}

export async function adminDeleteLiveRoom(id: string): Promise<void> {
  try { await api.delete(`/api/v1/admin/live/rooms/${id}`); } catch (e) { throw unwrapError(e); }
}

export function channelConvId(channelId: string): string {
  return `c_${channelId}`;
}

export async function addReaction(messageId: string, emoji: string): Promise<void> {
  try {
    await api.post(`/api/v1/messages/${messageId}/reactions`, { emoji });
  } catch (e) { throw unwrapError(e); }
}

export async function removeReaction(messageId: string, emoji: string): Promise<void> {
  try {
    await api.delete(`/api/v1/messages/${messageId}/reactions/${encodeURIComponent(emoji)}`);
  } catch (e) { throw unwrapError(e); }
}

export async function pinMessage(messageId: string): Promise<void> {
  try { await api.post(`/api/v1/messages/${messageId}/pin`); } catch (e) { throw unwrapError(e); }
}

export async function unpinMessage(messageId: string): Promise<void> {
  try { await api.delete(`/api/v1/messages/${messageId}/pin`); } catch (e) { throw unwrapError(e); }
}

export async function listPins(conversationId: string): Promise<Pin[]> {
  try {
    const res = await api.get<{ pins: Pin[] }>(`/api/v1/conversations/${encodeURIComponent(conversationId)}/pins`);
    return res.data.pins ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function markRead(conversationId: string, seq: number): Promise<number> {
  try {
    const res = await api.post<{ seq: number }>(`/api/v1/conversations/${encodeURIComponent(conversationId)}/read`, { seq });
    return res.data.seq;
  } catch (e) { throw unwrapError(e); }
}

// re-export type so callers don't need to import from @/types separately
export type { ReactionCount };

export interface SearchHit {
  id: string;
  conversationId: string;
  senderId: string;
  type: string;
  text: string;
  seq: number;
  createdAt: string;
}

export async function searchMessages(q: string, conversationId?: string): Promise<SearchHit[]> {
  if (!q.trim()) return [];
  try {
    const res = await api.get<{ hits: SearchHit[] }>('/api/v1/search/messages', {
      params: { q, conversationId, limit: 30 },
    });
    return res.data.hits ?? [];
  } catch (e) { throw unwrapError(e); }
}

export async function listGroups(): Promise<Group[]> {
  try {
    const res = await api.get<{ groups: Group[] }>('/api/v1/groups');
    return res.data.groups ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function createGroup(name: string): Promise<Group> {
  try {
    const res = await api.post<{ group: Group }>('/api/v1/groups', { name });
    return res.data.group;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function joinGroup(inviteCode: string): Promise<Group> {
  try {
    const res = await api.post<{ group: Group }>('/api/v1/groups/join', { inviteCode });
    return res.data.group;
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function listGroupMembers(groupId: string): Promise<GroupMember[]> {
  try {
    const res = await api.get<{ members: GroupMember[] }>(`/api/v1/groups/${groupId}/members`);
    return res.data.members ?? [];
  } catch (e) {
    throw unwrapError(e);
  }
}

export async function setMemberRole(groupId: string, userId: string, role: 0 | 1): Promise<void> {
  try {
    await api.patch(`/api/v1/groups/${groupId}/members/${userId}/role`, { role });
  } catch (e) { throw unwrapError(e); }
}

export async function kickMember(groupId: string, userId: string): Promise<void> {
  try {
    await api.delete(`/api/v1/groups/${groupId}/members/${userId}`);
  } catch (e) { throw unwrapError(e); }
}

export function privateConvId(a: string, b: string): string {
  const [x, y] = BigInt(a) < BigInt(b) ? [a, b] : [b, a];
  return `p_${x}_${y}`;
}

export function groupConvId(groupId: string): string {
  return `g_${groupId}`;
}
