export interface User {
  id: string;
  username: string;
  email: string;
  nickname: string;
  avatarUrl?: string;
  bio?: string;
  status: number;
  emailVerified: boolean;
  isAdmin: boolean;
  createdAt: string;
}

export interface SessionItem {
  id: string;
  device: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt: string;
  isCurrent: boolean;
}

export interface LiveRoom {
  id: string;
  ownerId: string;
  title: string;
  coverUrl?: string;
  category?: string;
  streamKey?: string; // only set on owner-fetched / create response
  status: number; // 0 idle, 1 live, 2 ended, 3 banned
  viewerCount: number;
  totalViews: number;
  isTest: boolean;       // true = host-only preview (hidden from /live/rooms discover)
  scheduledAt?: string;  // RFC 3339; set if this is a future stream
  startedAt?: string;
  endedAt?: string;
  createdAt: string;
}

export interface LiveDanmakuItem {
  id: string;
  roomId: string;
  senderId: string;
  text: string;
  color?: string;
  ts: string;       // ISO timestamp from server
}

export interface LiveRoomDetail {
  room: LiveRoom;
  rtmpUrl?: string;     // owner-only
  playbackUrl: string;  // visible to viewers
}

export interface DanmakuEvent {
  roomId: string;
  text: string;
  color?: string;
  senderId: string;
  ts: number;
}

export interface LoginResponse {
  accessToken: string;
  refreshToken: string;
  user: User;
}

export interface ApiError {
  code: number;
  message: string;
}

export interface Friend {
  id: string;
  username: string;
  nickname: string;
  avatarUrl?: string;
  remark?: string;
  isOnline?: boolean;
  createdAt: string;
}

export interface ReactionCount {
  emoji: string;
  count: number;
  userIds?: number[];
}

export interface ChatMessage {
  id: string;
  conversationId: string;
  senderId: string;
  type: string;
  content: { text?: string; [k: string]: unknown };
  seq: number;
  mentions?: number[];
  replyTo?: number;
  reactions?: ReactionCount[];
  isRecalled: boolean;
  createdAt: string;
}

export interface Pin {
  conversationId: string;
  messageId: string;
  pinnedBy: string;
  pinnedAt: string;
  message?: ChatMessage;
}

export interface Channel {
  id: string;
  groupId: string;
  type: number;
  name: string;
  topic?: string;
  position: number;
  createdAt: string;
}

export interface Group {
  id: string;
  type: number;
  name: string;
  iconUrl?: string;
  description?: string;
  announcement?: string;
  ownerId: string;
  memberCount: number;
  maxMembers: number;
  isPublic: boolean;
  inviteCode: string;
  createdAt: string;
}

export interface GroupMember {
  userId: string;
  username: string;
  nickname: string;
  avatarUrl?: string;
  role: number;
  joinedAt: string;
}

export interface WSEvent {
  type: string;
  ts: number;
  payload: any;
}
