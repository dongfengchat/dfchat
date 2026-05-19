export interface User {
  id: string;
  accountNo: string; // public 6+ digit number — what users see / type into login
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

  // Tier-C chat moderation. Always present (defaults: 0 / false / null).
  chatSubscribersOnly?: boolean;
  slowModeSeconds?: number;
  pinnedDanmakuText?: string;
  pinnedDanmakuSender?: string;
  pinnedDanmakuColor?: string;
  pinnedDanmakuAt?: string; // ISO timestamp

  // Admin-set ban reason. Surfaced in Studio when status=3 so the
  // streamer sees why they can't broadcast. Empty/missing on rooms
  // that have never been banned.
  bannedReason?: string;
}

export interface LiveDanmakuItem {
  id: string;
  roomId: string;
  senderId: string;
  // Sender's nickname + public account_no, captured at SELECT time by
  // the server's RecentDanmaku JOIN. Both can be empty if the sender's
  // account has been deleted — client renders blank name in that case.
  senderNickname?: string;
  senderAccountNo?: string;
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
  // Display labels for the chat side-panel. Populated server-side on
  // each broadcast (live.danmaku.recv) so clients can render
  // "<昵称> #<账号>" without a follow-up REST lookup per message.
  // Optional because messages from a since-deleted account, or
  // locally-echoed danmaku rendered before the WS round-trip, may
  // not have them.
  senderNickname?: string;
  senderAccountNo?: string;
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
  // Edit metadata — present on text messages that have been edited
  // within the 5-min window. Client renders "(已编辑)" when editedAt is
  // set. Absent on never-edited messages.
  editedAt?: string;
  editCount?: number;
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
