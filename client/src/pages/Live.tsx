import { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  ArrowLeft,
  Bell,
  BellOff,
  Calendar,
  Camera,
  Copy,
  Eye,
  EyeOff,
  Gauge,
  Heart,
  KeyRound,
  Link2,
  Loader2,
  Maximize2,
  Palette,
  Pencil,
  PictureInPicture2,
  Smile,
  Plus,
  RadioTower,
  RefreshCw,
  Send,
  Square,
  Trash2,
  Tv,
  Upload,
  Users,
  Video,
} from 'lucide-react';
import Hls from 'hls.js';
import {
  createLiveRoom,
  deleteLiveRoom,
  getLiveRoom,
  getLiveRoomOwner,
  listLiveRooms,
  listMyLiveRooms,
  rotateLiveStreamKey,
  setLiveRoomVisibility,
  stopLiveRoom,
  followLiveRoom,
  unfollowLiveRoom,
  getLiveRoomFollowStatus,
  listLiveDanmaku,
  setLiveRoomSchedule,
  listScheduledLiveRooms,
  updateLiveRoom,
  uploadBlob,
  banLiveUser,
} from '@/api/client';
import { useUserStore } from '@/store/userStore';
import { wsClient } from '@/ws/client';
import type { DanmakuEvent, LiveRoom, LiveRoomDetail } from '@/types';
import TitleBar from '@/components/TitleBar';
import Avatar from '@/components/ui/Avatar';
import { toast } from '@/components/ui/Toast';

// Curated emoji set for the danmaku/chat picker. Covers reactions, faces,
// gestures, animals, food + the few "stream culture" emojis (🔥 💯 🎉).
const COMMON_EMOJIS = [
  '😀','😂','🥹','😍','🥰','😘','😎','🤔',
  '😅','😭','😡','🤯','🥱','😴','🤤','🤓',
  '🤗','🤩','😋','😏','😬','🙄','🥳','🤝',
  '👍','👎','👏','🙏','💪','🙌','✌️','🤞',
  '❤️','💔','💕','💯','🔥','✨','🎉','🎊',
  '👀','💀','🤡','💩','🐶','🐱','🦁','🐼',
  '🍕','🍔','🍣','🍜','🍺','🍷','☕','🍰',
  '🚀','🌟','⭐','🌈','☀️','🌙','⚡','💎',
];

type Tab = 'discover' | 'studio';

export default function Live() {
  const navigate = useNavigate();
  const me = useUserStore((s) => s.user);
  const [tab, setTab] = useState<Tab>('discover');
  const [active, setActive] = useState<LiveRoom | null>(null); // currently watching

  if (!me) {
    navigate('/login', { replace: true });
    return null;
  }

  return (
    <div className="min-h-screen flex flex-col bg-bg-1 text-ink-1">
      <TitleBar title="东风快信 · 直播" />
      <header className="h-14 px-6 border-b border-bg-5/40 bg-bg-2/60 backdrop-blur flex items-center gap-3 shrink-0">
        <button onClick={() => navigate('/home')} className="btn-icon" title="返回聊天">
          <ArrowLeft size={18} />
        </button>
        <RadioTower size={18} className="text-brand-300" />
        <h1 className="text-base font-semibold">直播</h1>
        <div className="ml-4 flex gap-1">
          <TabBtn label="广场" active={tab === 'discover'} onClick={() => { setTab('discover'); setActive(null); }} />
          <TabBtn label="我的直播" active={tab === 'studio'} onClick={() => { setTab('studio'); setActive(null); }} />
        </div>
      </header>

      <main className="flex-1 min-h-0 overflow-hidden">
        {active ? (
          <Watch room={active} onBack={() => setActive(null)} />
        ) : tab === 'discover' ? (
          <Discover onOpen={(r) => setActive(r)} />
        ) : (
          <Studio onPreview={(r) => setActive(r)} />
        )}
      </main>
    </div>
  );
}

function TabBtn({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className={`px-3 py-1.5 rounded-md text-sm transition-colors ${
        active ? 'bg-bg-3 text-ink-1' : 'text-ink-3 hover:bg-bg-3/60'
      }`}
    >
      {label}
    </button>
  );
}

// ===== Discover ================================================

function Discover({ onOpen }: { onOpen: (r: LiveRoom) => void }) {
  const [rooms, setRooms] = useState<LiveRoom[] | null>(null);
  const [scheduled, setScheduled] = useState<LiveRoom[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [activeCategory, setActiveCategory] = useState<string>('全部');

  useEffect(() => {
    let cancelled = false;
    Promise.all([listLiveRooms(), listScheduledLiveRooms().catch(() => [])])
      .then(([live, sched]) => {
        if (cancelled) return;
        setRooms(live.rooms);
        setScheduled(sched);
      })
      .catch((e) => !cancelled && setError(e.message ?? '加载失败'));
    return () => { cancelled = true; };
  }, []);

  // Distinct category list (plus a "全部" sentinel for "no filter").
  const categories = useMemo(() => {
    const set = new Set<string>();
    rooms?.forEach((r) => { if (r.category) set.add(r.category); });
    return ['全部', ...Array.from(set).sort()];
  }, [rooms]);
  const filteredRooms = useMemo(() => {
    if (!rooms) return null;
    if (activeCategory === '全部') return rooms;
    return rooms.filter((r) => r.category === activeCategory);
  }, [rooms, activeCategory]);

  if (error) return <div className="p-8 text-accent-red">{error}</div>;
  if (rooms === null) return <div className="p-8 text-ink-3 flex items-center gap-2"><Loader2 size={16} className="animate-spin" /> 加载中…</div>;

  return (
    <div className="p-6 max-w-6xl mx-auto">
      {/* Scheduled / upcoming streams */}
      {scheduled.length > 0 && (
        <div className="mb-8">
          <h2 className="text-lg font-semibold mb-1 flex items-center gap-2">
            <Calendar size={16} className="text-brand-300" /> 即将开播
          </h2>
          <p className="text-sm text-ink-3 mb-4">主播预告的直播时间，到点可以来看</p>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
            {scheduled.map((r) => {
              const when = r.scheduledAt ? new Date(r.scheduledAt) : null;
              return (
                <div key={r.id} className="card p-3 flex gap-3 items-center anim-fade">
                  <div className="w-16 h-16 rounded-lg bg-bg-3 shrink-0 overflow-hidden flex items-center justify-center">
                    {r.coverUrl ? (
                      <img src={r.coverUrl} className="w-full h-full object-cover" alt="" />
                    ) : (
                      <Tv size={24} className="text-ink-4" />
                    )}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="font-medium truncate text-sm">{r.title}</div>
                    {when && (
                      <div className="text-xs text-brand-300 mt-0.5">
                        {when.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })}
                      </div>
                    )}
                    {r.category && <div className="text-[11px] text-ink-4">{r.category}</div>}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      <h2 className="text-lg font-semibold mb-1">正在直播</h2>
      <p className="text-sm text-ink-3 mb-4">点击进入直播间观看 + 发弹幕</p>

      {/* Category filter pills — only shows when rooms have at least one category. */}
      {categories.length > 1 && (
        <div className="flex flex-wrap gap-1.5 mb-4">
          {categories.map((c) => (
            <button
              key={c}
              onClick={() => setActiveCategory(c)}
              className={`px-3 py-1 rounded-full text-xs transition-colors ${
                activeCategory === c
                  ? 'bg-brand-500 text-white'
                  : 'bg-bg-3 text-ink-2 hover:bg-bg-4'
              }`}
            >
              {c}
            </button>
          ))}
        </div>
      )}

      {filteredRooms && filteredRooms.length === 0 && (
        <div className="card p-12 text-center text-ink-3 anim-fade">
          <Tv size={36} className="mx-auto mb-3 text-ink-4" />
          <div>{activeCategory === '全部' ? '当前没人在直播' : `没有「${activeCategory}」分类的直播`}</div>
          <div className="text-xs text-ink-4 mt-1">切换到「我的直播」自己开一个</div>
        </div>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        {(filteredRooms ?? []).map((r) => (
          <button
            key={r.id}
            onClick={() => onOpen(r)}
            className="card overflow-hidden text-left hover:border-brand-500/60 transition-colors"
          >
            <div className="aspect-video bg-bg-3 relative">
              {r.coverUrl ? (
                <img src={r.coverUrl} className="w-full h-full object-cover" alt="" />
              ) : (
                <div className="w-full h-full flex items-center justify-center text-ink-4">
                  <Tv size={40} />
                </div>
              )}
              <span className="absolute top-2 left-2 inline-flex items-center gap-1 px-2 py-0.5 rounded bg-accent-red text-white text-[11px] font-medium">
                <span className="w-1.5 h-1.5 rounded-full bg-white animate-pulse" /> LIVE
              </span>
              <span className="absolute bottom-2 right-2 inline-flex items-center gap-1 px-2 py-0.5 rounded bg-black/60 text-white text-[11px]">
                <Users size={11} /> {r.viewerCount}
              </span>
            </div>
            <div className="p-3">
              <div className="font-medium truncate">{r.title}</div>
              {r.category && <div className="text-xs text-ink-3 mt-0.5">{r.category}</div>}
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}

// ===== Watch (HLS + danmaku) ===================================

function Watch({ room, onBack }: { room: LiveRoom; onBack: () => void }) {
  const me = useUserStore((s) => s.user);
  const [detail, setDetail] = useState<LiveRoomDetail | null>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  const [danmaku, setDanmaku] = useState<DanmakuEvent[]>([]);
  const [text, setText] = useState('');
  // Historical replay was disabled to keep DVR storage in check; SRS dvr=off
  // and the recordings list UI has been removed. If you bring DVR back,
  // re-introduce listLiveRecordings + replayingId state here and the
  // recordings render block at the bottom of this component.
  // Real-time viewer count (seeded from the room prop, then updated by WS).
  const [viewerCount, setViewerCount] = useState<number>(room.viewerCount);
  // Follow state.
  const [following, setFollowing] = useState(false);
  const [followerCount, setFollowerCount] = useState(0);
  // User's preferred danmaku color, persisted to localStorage.
  const [myColor, setMyColor] = useState<string>(
    () => localStorage.getItem('danmaku.color') || '#ffffff',
  );
  const [showColorPicker, setShowColorPicker] = useState(false);
  const [showEmojiPicker, setShowEmojiPicker] = useState(false);
  const isOwner = !!me && me.id === room.ownerId;
  // Live-updated room state. Starts from prop, refreshed when the host
  // edits the room (we receive a `live.room.updated` WS event).
  const [liveRoom, setLiveRoom] = useState<LiveRoom>(room);
  const [buffering, setBuffering] = useState(false);
  // Quality picker: 'ld' = 480p transcoded (smooth), 'src' = host's original
  // upload (could be 1080p). Default 'ld' to save bandwidth — switching to
  // 'src' triggers a one-time consent dialog.
  const [quality, setQuality] = useState<'ld' | 'src'>('ld');
  const [showQualityMenu, setShowQualityMenu] = useState(false);
  const [showHDConsent, setShowHDConsent] = useState(false);
  const [inPiP, setInPiP] = useState(false);
  // Player-level error surface — replaces a black/buffering video with a
  // human-readable card. Cleared on a successful MANIFEST_LOADED.
  const [playerError, setPlayerError] = useState<string | null>(null);
  // Owner-only "force stop" busy flag.
  const [stopping, setStopping] = useState(false);

  // refreshPlayback re-fetches the detail to grab a fresh signed URL
  // (server token TTL is 1 h). Called automatically when hls.js hits a
  // 401/403 — the URL has gone stale either because exp passed or
  // because the host rotated the stream key.
  const refreshPlayback = async () => {
    try {
      const fetcher = !!me && me.id === room.ownerId ? getLiveRoomOwner : getLiveRoom;
      const fresh = await fetcher(room.id);
      setDetail(fresh);
    } catch (err: any) {
      setPlayerError(err?.message ?? '直播间已下线');
    }
  };

  // Fetch the playback detail. Owners use the privileged endpoint so they
  // can preview their own test-mode room (public endpoint 404s on test rooms).
  useEffect(() => {
    let cancelled = false;
    const isOwner = !!me && me.id === room.ownerId;
    const fetcher = isOwner ? getLiveRoomOwner : getLiveRoom;
    fetcher(room.id)
      .then((d) => !cancelled && setDetail(d))
      .catch(() => {});
    return () => { cancelled = true; };
  }, [room.id, room.ownerId, me]);

  // Wire <video> to the live HLS playlist (DVR replays disabled for now).
  // Re-runs only when the URL or selected quality changes.
  useEffect(() => {
    if (!videoRef.current) return;
    const v = videoRef.current;

    hlsRef.current?.destroy();
    hlsRef.current = null;
    v.removeAttribute('src');
    v.load();

    if (!detail?.playbackUrl) return;
    // Pick the variant URL. SRS transcodes <key>.m3u8 → <key>_ld.m3u8 (480p).
    const playUrl = quality === 'ld'
      ? detail.playbackUrl.replace(/\.m3u8$/, '_ld.m3u8')
      : detail.playbackUrl;
    // Safari can play HLS natively; everyone else needs hls.js.
    if (v.canPlayType('application/vnd.apple.mpegurl')) {
      v.src = playUrl;
    } else if (Hls.isSupported()) {
      const hls = new Hls({ lowLatencyMode: true, liveSyncDuration: 2 });
      hlsRef.current = hls;
      hls.loadSource(playUrl);
      hls.attachMedia(v);
      // Bounded retry counter so a permanently-offline stream doesn't
      // hammer the network indefinitely. Resets on the first successful
      // segment load (MANIFEST_LOADED) so a transient blip mid-stream
      // doesn't trip the limit. After maxRetries consecutive failures
      // we fall back to the "主播离线" placeholder.
      let networkRetries = 0;
      const maxRetries = 8;
      hls.on(Hls.Events.MANIFEST_LOADED, () => {
        networkRetries = 0;
        setPlayerError(null);
      });
      hls.on(Hls.Events.ERROR, (_evt, data) => {
        if (!data.fatal) return;
        switch (data.type) {
          case Hls.ErrorTypes.NETWORK_ERROR:
            // Token may have expired (we send the URL with ?token=&exp=
            // and nginx returns 401 past TTL) — refetch detail to grab
            // a fresh URL and rebuild the player.
            if (data.response?.code === 401 || data.response?.code === 403) {
              setPlayerError('回话过期，正在刷新…');
              void refreshPlayback();
              return;
            }
            networkRetries++;
            if (networkRetries > maxRetries) {
              setPlayerError('主播离线（无法连接到直播流）');
              return;
            }
            setTimeout(() => hls.startLoad(), 2000);
            break;
          case Hls.ErrorTypes.MEDIA_ERROR:
            // Codec / decoder hiccup. hls.js can self-heal these.
            try { hls.recoverMediaError(); } catch { /* give up */
              setPlayerError('播放器解码失败，请刷新页面');
            }
            break;
          default:
            setPlayerError('播放器异常，请刷新页面');
        }
      });
    }
    return () => {
      hlsRef.current?.destroy();
      hlsRef.current = null;
    };
  }, [detail?.playbackUrl, quality]);

  // Load recent danmaku history (so late-joiners see the recent chat).
  useEffect(() => {
    let cancelled = false;
    listLiveDanmaku(room.id, 50)
      .then((items) => {
        if (cancelled) return;
        setDanmaku(items.map((d) => ({
          roomId: d.roomId,
          text: d.text,
          color: d.color,
          senderId: d.senderId,
          ts: new Date(d.ts).getTime(),
        })));
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [room.id]);

  // Subscribe to danmaku + viewer-count updates for this room.
  useEffect(() => {
    if (!room.id) return;
    wsClient.send('live.subscribe', { roomId: room.id });
    const off = wsClient.on((ev) => {
      if (ev.type === 'live.danmaku.recv') {
        const d = ev.payload as DanmakuEvent;
        if (d.roomId !== room.id) return;
        setDanmaku((cur) => [...cur.slice(-150), d]);
      } else if (ev.type === 'live.viewer.count') {
        const p = ev.payload as { roomId: string; count: number };
        if (p.roomId === room.id) setViewerCount(p.count);
      } else if (ev.type === 'live.room.updated') {
        const p = ev.payload as Partial<LiveRoom> & { roomId: string };
        if (p.roomId !== room.id) return;
        setLiveRoom((r) => ({ ...r, ...p }));
        // If the host moved this room into test mode while we were watching,
        // a follower can't reach it anymore — close gracefully on next tick.
        if (p.isTest && !isOwner) {
          toast('主播已切回试播模式，即将退出', 'info');
          setTimeout(onBack, 1500);
        }
      } else if (ev.type === 'live.danmaku.rejected') {
        const p = ev.payload as { reason?: string };
        const msg = p.reason === 'banned'
          ? '你已被禁言，无法发送弹幕'
          : p.reason === 'not_subscribed'
          ? '请等待页面准备就绪后再发送'
          : '弹幕被拒绝';
        toast(msg, 'error');
      } else if (ev.type === 'live.room.deleted') {
        // Owner dissolved the room from elsewhere (or the SRS reconcile
        // loop force-ended a zombie). Tear down + bounce out.
        const p = ev.payload as { roomId: string };
        if (p.roomId === room.id) {
          toast('直播间已被关闭', 'info');
          setTimeout(onBack, 1200);
        }
      }
    });
    return () => {
      off();
      wsClient.send('live.unsubscribe', { roomId: room.id });
    };
  }, [room.id]);

  // Follow status (refreshed when room changes).
  useEffect(() => {
    if (isOwner) return; // owner doesn't follow themselves
    let cancelled = false;
    getLiveRoomFollowStatus(room.id)
      .then((s) => {
        if (cancelled) return;
        setFollowing(s.following);
        setFollowerCount(s.count);
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [room.id, isOwner]);

  async function toggleFollow() {
    try {
      if (following) {
        await unfollowLiveRoom(room.id);
        setFollowing(false);
        setFollowerCount((c) => Math.max(0, c - 1));
      } else {
        await followLiveRoom(room.id);
        setFollowing(true);
        setFollowerCount((c) => c + 1);
        toast('已关注，主播开播会通知你', 'success');
      }
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    }
  }

  function shareLink() {
    const url = `https://dfchat.chat/live/${room.id}`;
    navigator.clipboard.writeText(url).then(() => toast('已复制直播间链接', 'success'));
  }

  function pickColor(c: string) {
    setMyColor(c);
    localStorage.setItem('danmaku.color', c);
    setShowColorPicker(false);
  }

  function sendDanmaku() {
    const t = text.trim();
    if (!t || !me) return;
    const ok = wsClient.send('live.danmaku.send', { roomId: room.id, text: t, color: myColor });
    if (!ok) {
      toast('网络中断中，正在重连。请稍后再发', 'error');
      return;
    }
    // Server skips echo to the sender to avoid dupes; render ours locally.
    setDanmaku((cur) => [...cur, {
      roomId: room.id,
      text: t,
      color: myColor,
      senderId: me.id,
      ts: Date.now(),
    }]);
    setText('');
  }

  function pickQuality(q: 'ld' | 'src') {
    setShowQualityMenu(false);
    if (q === quality) return;
    if (q === 'src') {
      // 1080p / source could be 5+ Mbps. Require explicit "agree" once,
      // remember the choice in localStorage.
      const agreed = localStorage.getItem('live.hd-agreed') === '1';
      if (!agreed) {
        setShowHDConsent(true);
        return;
      }
    }
    setQuality(q);
  }

  function confirmHD() {
    localStorage.setItem('live.hd-agreed', '1');
    setShowHDConsent(false);
    setQuality('src');
  }

  async function togglePiP() {
    const v = videoRef.current;
    if (!v) return;
    try {
      if (document.pictureInPictureElement === v) {
        await document.exitPictureInPicture();
      } else {
        await v.requestPictureInPicture();
      }
    } catch (e: any) {
      toast(e?.message || '画中画不可用', 'error');
    }
  }

  // Wire PiP enter/leave events so the button state stays in sync even
  // when the user closes the floating window manually.
  useEffect(() => {
    const v = videoRef.current;
    if (!v) return;
    const onEnter = () => setInPiP(true);
    const onLeave = () => setInPiP(false);
    const onWait = () => setBuffering(true);
    const onPlaying = () => setBuffering(false);
    v.addEventListener('enterpictureinpicture', onEnter);
    v.addEventListener('leavepictureinpicture', onLeave);
    v.addEventListener('waiting', onWait);
    v.addEventListener('stalled', onWait);
    v.addEventListener('playing', onPlaying);
    v.addEventListener('canplay', onPlaying);
    return () => {
      v.removeEventListener('enterpictureinpicture', onEnter);
      v.removeEventListener('leavepictureinpicture', onLeave);
      v.removeEventListener('waiting', onWait);
      v.removeEventListener('stalled', onWait);
      v.removeEventListener('playing', onPlaying);
      v.removeEventListener('canplay', onPlaying);
    };
  }, []);

  // Keyboard shortcuts: F = toggle fullscreen, M = mute, space = play/pause.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const t = e.target as HTMLElement | null;
      // Skip when typing in an input or textarea (danmaku field).
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA')) return;
      const v = videoRef.current;
      if (!v) return;
      if (e.key === 'f' || e.key === 'F') {
        e.preventDefault();
        if (document.fullscreenElement) document.exitFullscreen();
        else v.requestFullscreen().catch(() => {});
      } else if (e.key === 'm' || e.key === 'M') {
        e.preventDefault();
        v.muted = !v.muted;
      } else if (e.code === 'Space') {
        e.preventDefault();
        if (v.paused) v.play().catch(() => {});
        else v.pause();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  async function takeScreenshot() {
    const v = videoRef.current;
    if (!v || !v.videoWidth) {
      toast('视频还没开始播放', 'error');
      return;
    }
    const canvas = document.createElement('canvas');
    canvas.width = v.videoWidth;
    canvas.height = v.videoHeight;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    ctx.drawImage(v, 0, 0);
    canvas.toBlob(async (blob) => {
      if (!blob) return;
      // 1. Save copy to disk (one-click share later)
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
      a.download = `dfchat-live-${stamp}.png`;
      a.click();
      URL.revokeObjectURL(url);
      // 2. Try clipboard copy as a bonus (only works on secure contexts +
      // when permitted; failure is silent so the file save still happens).
      try {
        if (navigator.clipboard && (window as any).ClipboardItem) {
          await navigator.clipboard.write([
            new (window as any).ClipboardItem({ 'image/png': blob }),
          ]);
          toast('截图已保存 + 复制到剪贴板', 'success');
          return;
        }
      } catch { /* fall through */ }
      toast('截图已保存到下载', 'success');
    }, 'image/png');
  }

  return (
    <div className="grid grid-cols-1 lg:grid-cols-[1fr_320px] gap-4 p-4 h-full overflow-hidden">
      <div className="flex flex-col min-h-0">
        <button onClick={onBack} className="self-start mb-2 text-sm text-ink-3 hover:text-ink-1 flex items-center gap-1">
          <ArrowLeft size={14} /> 返回列表
        </button>

        <div className="relative bg-black rounded-xl overflow-hidden aspect-video">
          <video
            ref={videoRef}
            controls
            autoPlay
            muted
            playsInline
            className="w-full h-full object-contain bg-black"
          />
          {/* Scrolling danmaku overlay — only the last ~20 fly across. */}
          <DanmakuLayer items={danmaku} />

          {/* Buffering indicator. Shown on <video> waiting/stalled events. */}
          {buffering && !playerError && (
            <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
              <div className="bg-black/60 px-3 py-1.5 rounded-full text-xs text-white flex items-center gap-1.5">
                <Loader2 size={12} className="animate-spin" /> 缓冲中…
              </div>
            </div>
          )}

          {/* Player-level error / offline placeholder. Shown when hls.js
              hits an unrecoverable fatal (max retries exhausted) or when
              the API tells us the room is offline. Clears on the next
              successful manifest load. */}
          {playerError && (
            <div className="absolute inset-0 flex items-center justify-center bg-black/80 z-10">
              <div className="text-center max-w-md px-6">
                <div className="text-6xl mb-3">📡</div>
                <div className="text-base text-ink-1 font-medium">{playerError}</div>
                <div className="text-xs text-ink-3 mt-2">
                  {liveRoom.status === 1
                    ? '主播的连接可能短暂中断，可以稍候刷新页面再试'
                    : liveRoom.status === 2
                    ? '本场直播已结束'
                    : '尚未开播 — 关注主播会在开播时通知你'}
                </div>
                <button
                  onClick={() => { setPlayerError(null); void refreshPlayback(); }}
                  className="mt-4 inline-flex items-center gap-1 text-xs px-3 py-1.5 rounded bg-bg-3 text-ink-1 hover:bg-bg-4 transition-colors"
                >
                  <RefreshCw size={12} /> 重试
                </button>
              </div>
            </div>
          )}

          {/* Top-right toolbar: quality + PiP + screenshot. */}
          <div className="absolute top-2 right-2 flex gap-1.5 z-20">
              <div className="relative">
                <button
                  onClick={() => setShowQualityMenu((v) => !v)}
                  className="px-2 py-1 rounded bg-black/55 hover:bg-black/75 text-white text-xs inline-flex items-center gap-1"
                  title="切换清晰度"
                >
                  <Gauge size={12} />
                  {quality === 'ld' ? '流畅' : '原画'}
                </button>
                {showQualityMenu && (
                  <div className="absolute top-full right-0 mt-1 card p-1 min-w-[120px] shadow-pop">
                    {(['ld', 'src'] as const).map((q) => (
                      <button
                        key={q}
                        onClick={() => pickQuality(q)}
                        className={`w-full text-left px-3 py-1.5 rounded text-sm transition-colors ${
                          q === quality ? 'bg-brand-500/20 text-brand-300' : 'hover:bg-bg-3'
                        }`}
                      >
                        {q === 'ld' ? '流畅 · 480p · 省流量' : '原画 · 高清 · 大流量'}
                      </button>
                    ))}
                  </div>
                )}
              </div>
              <button
                onClick={togglePiP}
                className={`p-1.5 rounded text-white text-xs ${inPiP ? 'bg-brand-500' : 'bg-black/55 hover:bg-black/75'}`}
                title={inPiP ? '退出画中画' : '画中画'}
              >
                <PictureInPicture2 size={14} />
              </button>
              <button
                onClick={takeScreenshot}
                className="p-1.5 rounded bg-black/55 hover:bg-black/75 text-white text-xs"
                title="截图保存"
              >
                <Camera size={14} />
              </button>
            </div>

          {/* 1080p / source quality consent dialog. Shown once per device. */}
          {showHDConsent && (
            <div className="absolute inset-0 bg-black/70 flex items-center justify-center z-30 p-6">
              <div className="card max-w-sm p-5 space-y-3">
                <h3 className="text-base font-semibold flex items-center gap-2">
                  <Maximize2 size={16} className="text-brand-300" /> 切换到原画?
                </h3>
                <p className="text-sm text-ink-2">
                  原画跟随主播实际推流码率（最高可能 <strong>5 Mbps+</strong>）。
                </p>
                <ul className="text-xs text-ink-3 space-y-1 list-disc pl-4">
                  <li>WiFi 环境完全没问题</li>
                  <li>移动数据 / 弱网建议保持<em className="text-brand-300 not-italic">流畅</em>，否则可能卡顿 + 烧流量</li>
                </ul>
                <div className="flex gap-2 justify-end pt-1">
                  <button onClick={() => setShowHDConsent(false)} className="btn-secondary text-xs">
                    取消，继续流畅
                  </button>
                  <button onClick={confirmHD} className="btn-primary text-xs">
                    我了解，切原画
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>

        <div className="mt-3 px-1">
          <h2 className="text-lg font-semibold">{liveRoom.title}</h2>
          <div className="text-xs text-ink-3 mt-1 flex items-center gap-3">
            {liveRoom.category && <span>{liveRoom.category}</span>}
            <span className="inline-flex items-center gap-1 text-accent-red">
              <span className="w-1.5 h-1.5 rounded-full bg-accent-red animate-pulse" /> 直播中
            </span>
            <span className="inline-flex items-center gap-1"><Users size={12} /> {viewerCount}</span>
            {!isOwner && (
              <>
                <button
                  onClick={toggleFollow}
                  className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded transition-colors ${
                    following
                      ? 'bg-bg-3 text-ink-2 hover:bg-bg-4'
                      : 'bg-accent-red/20 text-accent-red hover:bg-accent-red/30'
                  }`}
                  title={following ? '已关注，开播会通知' : '关注主播，开播提醒'}
                >
                  <Heart size={11} fill={following ? 'currentColor' : 'none'} />
                  {following ? `已关注 ${followerCount}` : `关注 ${followerCount > 0 ? followerCount : ''}`}
                </button>
              </>
            )}
            <button
              onClick={shareLink}
              className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-bg-3 text-ink-2 hover:bg-bg-4 transition-colors"
              title="复制直播间链接"
            >
              <Link2 size={11} /> 分享
            </button>
            {isOwner && liveRoom.status === 1 && (
              <button
                onClick={async () => {
                  if (!confirm('结束这场直播？\n\n服务端会立刻把状态置为「已结束」，观众端的播放器将断开。\n如果 OBS 还在推流，下一次 SRS 心跳会重新拉起 — 想彻底结束请先停 OBS。')) return;
                  setStopping(true);
                  try {
                    await stopLiveRoom(room.id);
                    toast('已结束直播', 'success');
                  } catch (err: any) {
                    toast(err?.message ?? '结束失败', 'error');
                  } finally {
                    setStopping(false);
                  }
                }}
                disabled={stopping}
                className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-accent-red/20 text-accent-red hover:bg-accent-red/30 transition-colors disabled:opacity-50"
                title="手动结束（OBS 静默崩溃时用）"
              >
                {stopping ? <Loader2 size={11} className="animate-spin" /> : <Square size={11} />} 结束直播
              </button>
            )}
          </div>
        </div>

        {/* Historical recordings UI removed alongside DVR — see comment in
            Watch() state declarations. */}
      </div>

      {/* Danmaku feed + input */}
      <aside className="card flex flex-col min-h-0">
        <div className="px-3 py-2 border-b border-bg-5/40 text-xs uppercase tracking-wider text-ink-3">弹幕</div>
        <div className="flex-1 overflow-y-auto p-3 space-y-1">
          {danmaku.length === 0 && (
            <div className="text-xs text-ink-4 text-center py-6">还没有弹幕，发第一条吧</div>
          )}
          {danmaku.slice(-100).map((d, i) => (
            <div key={i} className="group text-sm text-ink-2 flex items-start gap-1.5 hover:bg-bg-3/40 rounded px-1 py-0.5">
              <span className="text-ink-4 text-[11px] shrink-0 mt-0.5">{new Date(d.ts).toLocaleTimeString().slice(0, 5)}</span>
              <span className="flex-1 min-w-0 break-words" style={d.color ? { color: d.color } : undefined}>
                {d.text}
              </span>
              {isOwner && me && d.senderId !== me.id && (
                <button
                  onClick={async () => {
                    const action = window.prompt(
                      `对用户 ${d.senderId} 的操作:\n  1 = 禁言（不能再发弹幕，可继续观看）\n  2 = 踢出（断开 WS 连接）\n  其他 = 取消`,
                      '1',
                    );
                    if (action !== '1' && action !== '2') return;
                    try {
                      await banLiveUser(room.id, d.senderId, action === '2', '主播屏蔽');
                      toast(action === '2' ? '已踢出该用户' : '已禁言该用户', 'success');
                    } catch (e: any) {
                      toast(e.message ?? '操作失败', 'error');
                    }
                  }}
                  className="opacity-0 group-hover:opacity-100 transition-opacity btn-icon w-6 h-6 text-accent-red shrink-0"
                  title="禁言 / 踢出"
                >
                  <BellOff size={11} />
                </button>
              )}
            </div>
          ))}
        </div>
        <div className="p-3 border-t border-bg-5/40 flex gap-2 relative">
          <button
            onClick={() => { setShowColorPicker((v) => !v); setShowEmojiPicker(false); }}
            className="btn-icon w-9 h-9 shrink-0"
            title="弹幕颜色"
            style={{ color: myColor }}
          >
            <Palette size={14} />
          </button>
          <button
            onClick={() => { setShowEmojiPicker((v) => !v); setShowColorPicker(false); }}
            className="btn-icon w-9 h-9 shrink-0"
            title="表情"
          >
            <Smile size={14} />
          </button>
          {showEmojiPicker && (
            <div className="absolute bottom-14 left-3 z-10 card p-2 shadow-pop w-[260px]">
              <div className="grid grid-cols-8 gap-0.5">
                {COMMON_EMOJIS.map((em) => (
                  <button
                    key={em}
                    onClick={() => { setText((t) => t + em); }}
                    className="w-7 h-7 rounded hover:bg-bg-3 text-base"
                    type="button"
                  >
                    {em}
                  </button>
                ))}
              </div>
            </div>
          )}
          {showColorPicker && (
            <div className="absolute bottom-14 left-3 z-10 card p-2 grid grid-cols-6 gap-1 shadow-pop">
              {['#ffffff', '#8eaaff', '#3a64ee', '#3ba55c', '#f5b042',
                '#ed7d2b', '#ef4444', '#a64dd1', '#6f4dd1', '#42d4f4',
                '#f97316', '#facc15'].map((c) => (
                <button
                  key={c}
                  onClick={() => pickColor(c)}
                  className="w-6 h-6 rounded border border-bg-5/40 hover:scale-110 transition-transform"
                  style={{ background: c }}
                  aria-label={c}
                />
              ))}
            </div>
          )}
          <input
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && sendDanmaku()}
            placeholder="发条弹幕…"
            maxLength={100}
            className="input flex-1"
            style={{ caretColor: myColor }}
          />
          <button onClick={sendDanmaku} disabled={!text.trim()} className="btn-primary">
            <Send size={14} />
          </button>
        </div>
      </aside>
    </div>
  );
}

function DanmakuLayer({ items }: { items: DanmakuEvent[] }) {
  // Show the last ~12 as floating bullets across the top half.
  const visible = items.slice(-12);
  return (
    <div className="absolute inset-0 pointer-events-none overflow-hidden">
      {visible.map((d, i) => (
        <div
          key={d.ts + '-' + i}
          className="absolute whitespace-nowrap text-white drop-shadow-md text-sm font-medium animate-danmaku"
          style={{
            top: `${(i % 6) * 12 + 6}%`,
            color: d.color || '#fff',
            // re-trigger animation per item by giving each a unique key
          }}
        >
          {d.text}
        </div>
      ))}
      <style>{`
        @keyframes danmaku-fly {
          from { transform: translateX(100vw); }
          to   { transform: translateX(-100%); }
        }
        .animate-danmaku {
          animation: danmaku-fly 8s linear forwards;
          text-shadow: 0 1px 3px rgba(0,0,0,0.9);
        }
      `}</style>
    </div>
  );
}

// ===== Studio ==================================================

function Studio({ onPreview }: { onPreview: (r: LiveRoom) => void }) {
  const [rooms, setRooms] = useState<LiveRoom[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [title, setTitle] = useState('');
  const [category, setCategory] = useState('');
  const [showCreate, setShowCreate] = useState(false);

  async function refresh() {
    setError(null);
    try {
      setRooms(await listMyLiveRooms());
    } catch (e: any) {
      setError(e.message ?? '加载失败');
    }
  }
  useEffect(() => { refresh(); }, []);

  async function submitCreate() {
    if (!title.trim()) return;
    setCreating(true);
    try {
      await createLiveRoom(title.trim(), category.trim() || undefined);
      setTitle(''); setCategory(''); setShowCreate(false);
      toast('已创建直播间', 'success');
      await refresh();
    } catch (e: any) {
      toast(e.message ?? '创建失败', 'error');
    } finally {
      setCreating(false);
    }
  }

  if (error) return <div className="p-8 text-accent-red">{error}</div>;

  return (
    <div className="p-6 max-w-5xl mx-auto">
      <div className="flex items-center mb-6">
        <div>
          <h2 className="text-lg font-semibold">我的直播间</h2>
          <p className="text-sm text-ink-3 mt-1">用 OBS 推流到这里，全网即可观看</p>
        </div>
        <button onClick={() => setShowCreate((v) => !v)} className="btn-primary ml-auto">
          <Plus size={14} /> 新建直播间
        </button>
      </div>

      {showCreate && (
        <div className="card p-5 mb-6 space-y-3 anim-fade">
          <div>
            <label className="text-xs text-ink-3">直播间标题</label>
            <input className="input mt-1" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="例如：周末摸鱼直播" maxLength={128} />
          </div>
          <div>
            <label className="text-xs text-ink-3">分类（可选）</label>
            <input className="input mt-1" value={category} onChange={(e) => setCategory(e.target.value)} placeholder="科技 / 游戏 / 闲聊" maxLength={32} />
          </div>
          <div className="text-[11px] text-ink-4 bg-bg-1/50 rounded px-2 py-1.5 -mt-1">
            💡 新房间默认进入<strong className="text-brand-300">试播模式</strong>，只有你自己能看到。
            推流过自己检查画质、麦克风、滤镜没问题后，再点「上线公开」让所有人看见。
          </div>
          <div className="flex gap-2 justify-end">
            <button onClick={() => setShowCreate(false)} className="btn-secondary">取消</button>
            <button onClick={submitCreate} disabled={creating || !title.trim()} className="btn-primary">
              {creating ? <Loader2 size={14} className="animate-spin" /> : null} 创建
            </button>
          </div>
        </div>
      )}

      {rooms === null ? (
        <div className="text-ink-3 flex items-center gap-2"><Loader2 size={16} className="animate-spin" /> 加载中…</div>
      ) : rooms.length === 0 ? (
        <div className="card p-12 text-center text-ink-3">
          <Video size={36} className="mx-auto mb-3 text-ink-4" />
          <div>还没有创建直播间</div>
        </div>
      ) : (
        <div className="space-y-3">
          {rooms.map((r) => <StudioRoomCard key={r.id} room={r} onChanged={refresh} onPreview={onPreview} />)}
        </div>
      )}
    </div>
  );
}

function StudioRoomCard({ room, onChanged, onPreview }: {
  room: LiveRoom;
  onChanged: () => void;
  onPreview: (r: LiveRoom) => void;
}) {
  const [detail, setDetail] = useState<LiveRoomDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [showKey, setShowKey] = useState(false);
  const [busy, setBusy] = useState(false);
  const [editOpen, setEditOpen] = useState(false);

  async function load() {
    setLoading(true);
    try {
      setDetail(await getLiveRoomOwner(room.id));
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => { load(); /* eslint-disable-next-line */ }, [room.id]);

  function copy(text: string, label: string) {
    navigator.clipboard.writeText(text).then(() => toast(`已复制${label}`, 'success'));
  }

  async function rotate() {
    if (!window.confirm('旋转密钥后，正在用旧密钥推流的 OBS 会立即断开。继续？')) return;
    try {
      await rotateLiveStreamKey(room.id);
      toast('已生成新密钥', 'success');
      await load();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    }
  }

  async function remove() {
    if (!window.confirm(`删除直播间「${room.title}」？录像也会一同删除。`)) return;
    try {
      await deleteLiveRoom(room.id);
      toast('已删除', 'success');
      onChanged();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    }
  }

  async function publish() {
    if (!window.confirm('上线公开后，所有用户都能在「广场」看到你的直播。继续？')) return;
    setBusy(true);
    try {
      await setLiveRoomVisibility(room.id, false);
      toast('已上线公开，广场可见', 'success');
      onChanged();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    } finally {
      setBusy(false);
    }
  }

  async function unpublish() {
    setBusy(true);
    try {
      await setLiveRoomVisibility(room.id, true);
      toast('已下线，回到试播模式', 'success');
      onChanged();
    } catch (e: any) {
      toast(e.message ?? '操作失败', 'error');
    } finally {
      setBusy(false);
    }
  }

  const isLive = room.status === 1;
  const isTest = room.isTest;
  const rtmpUrl = detail?.rtmpUrl ?? '';
  const streamKey = detail?.room.streamKey ?? '';
  // OBS expects server URL minus the stream key; the key is entered separately.
  const obsServer = rtmpUrl.replace(/\/[^/]+$/, '');

  return (
    <div className="card p-5 anim-fade">
      <div className="flex items-start gap-4">
        <Avatar name={room.title} size={48} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <h3 className="font-semibold truncate">{room.title}</h3>
            {isLive ? (
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-accent-red/20 text-accent-red text-[11px] font-medium">
                <span className="w-1.5 h-1.5 rounded-full bg-accent-red animate-pulse" /> 推流中
              </span>
            ) : (
              <span className="px-2 py-0.5 rounded bg-bg-3 text-ink-3 text-[11px]">未推流</span>
            )}
            {isTest ? (
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-brand-500/20 text-brand-300 text-[11px] font-medium">
                <EyeOff size={10} /> 试播 · 仅自己可见
              </span>
            ) : (
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-accent-green/20 text-accent-green text-[11px] font-medium">
                <Eye size={10} /> 公开 · 广场可见
              </span>
            )}
          </div>
          <div className="text-xs text-ink-3 mt-0.5">
            {room.category || '未分类'} · 累计观看 {room.totalViews} · 创建于 {new Date(room.createdAt).toLocaleDateString()}
          </div>
        </div>
        <button onClick={() => setEditOpen(true)} className="btn-icon" title="编辑"><Pencil size={16} /></button>
        <button onClick={remove} className="btn-icon text-accent-red" title="删除"><Trash2 size={16} /></button>
      </div>

      {editOpen && (
        <EditRoomDialog
          room={room}
          onClose={() => setEditOpen(false)}
          onSaved={() => { setEditOpen(false); onChanged(); }}
        />
      )}

      {/* Action row: preview + publish/unpublish */}
      <div className="mt-3 flex flex-wrap gap-2">
        <button
          onClick={() => onPreview(room)}
          disabled={!isLive}
          className="btn-secondary text-xs"
          title={isLive ? '观看自己的推流效果' : '需要先开始推流才能预览'}
        >
          <Eye size={14} /> 预览画面
        </button>
        {isTest ? (
          <button
            onClick={publish}
            disabled={busy || !isLive}
            className="btn-primary text-xs"
            title={isLive ? '推到广场让所有人能看' : '需要先用 OBS 推流'}
          >
            {busy ? <Loader2 size={14} className="animate-spin" /> : <Upload size={14} />}
            上线公开
          </button>
        ) : (
          <button
            onClick={unpublish}
            disabled={busy}
            className="btn-secondary text-xs"
          >
            {busy ? <Loader2 size={14} className="animate-spin" /> : <EyeOff size={14} />}
            下线回试播
          </button>
        )}
      </div>

      {loading ? (
        <div className="mt-4 text-sm text-ink-4 flex items-center gap-2"><Loader2 size={14} className="animate-spin" /> 加载推流信息…</div>
      ) : (
        <div className="mt-4 space-y-3 bg-bg-1/50 rounded-lg p-4">
          <div>
            <div className="text-xs text-ink-3 mb-1">OBS 服务器 URL</div>
            <div className="flex items-center gap-2">
              <code className="flex-1 text-xs bg-bg-3 px-2 py-1.5 rounded font-mono truncate">{obsServer}</code>
              <button onClick={() => copy(obsServer, '服务器 URL')} className="btn-icon w-8 h-8"><Copy size={14} /></button>
            </div>
          </div>
          <div>
            <div className="text-xs text-ink-3 mb-1 flex items-center gap-2">
              推流密钥
              <button onClick={() => setShowKey((v) => !v)} className="text-[10px] underline">
                {showKey ? '隐藏' : '显示'}
              </button>
              <button onClick={rotate} className="ml-auto text-[10px] text-accent-amber inline-flex items-center gap-1">
                <RefreshCw size={10} /> 重置
              </button>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 text-xs bg-bg-3 px-2 py-1.5 rounded font-mono truncate">
                {showKey ? streamKey : '•'.repeat(Math.min(streamKey.length, 32))}
              </code>
              <button onClick={() => copy(streamKey, '推流密钥')} className="btn-icon w-8 h-8"><KeyRound size={14} /></button>
            </div>
          </div>
          <div className="text-[11px] text-ink-4 pt-1 border-t border-bg-5/30">
            <b>使用 OBS：</b>设置 → 推流 → 服务"自定义" → 把上面两项粘进去 → 开始推流
          </div>
        </div>
      )}
    </div>
  );
}

// ===== EditRoomDialog ==============================================

function EditRoomDialog({ room, onClose, onSaved }: {
  room: LiveRoom;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [title, setTitle] = useState(room.title);
  const [category, setCategory] = useState(room.category ?? '');
  const [coverUrl, setCoverUrl] = useState(room.coverUrl ?? '');
  // Convert RFC 3339 → "YYYY-MM-DDTHH:mm" for <input type="datetime-local">.
  const initialSchedule = room.scheduledAt
    ? new Date(room.scheduledAt).toISOString().slice(0, 16)
    : '';
  const [schedule, setSchedule] = useState(initialSchedule);
  const [uploading, setUploading] = useState(false);
  const [saving, setSaving] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  async function pickCover(e: React.ChangeEvent<HTMLInputElement>) {
    const f = e.target.files?.[0];
    if (!f) return;
    if (!f.type.startsWith('image/')) {
      toast('请选择图片文件', 'error');
      return;
    }
    setUploading(true);
    try {
      const uploaded = await uploadBlob(f, f.name, 'image');
      setCoverUrl(uploaded.url);
      toast('封面已上传', 'success');
    } catch (err: any) {
      toast(err.message ?? '上传失败', 'error');
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = '';
    }
  }

  async function save() {
    setSaving(true);
    try {
      await updateLiveRoom(room.id, {
        title: title.trim() || undefined,
        category: category.trim(),
        coverUrl: coverUrl,
      });
      // Schedule is on a separate endpoint.
      const next = schedule ? new Date(schedule).toISOString() : null;
      const current = room.scheduledAt ?? null;
      if (next !== current) {
        await setLiveRoomSchedule(room.id, next);
      }
      toast('已保存', 'success');
      onSaved();
    } catch (e: any) {
      toast(e.message ?? '保存失败', 'error');
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-30 bg-black/60 flex items-center justify-center p-4 anim-fade" onClick={onClose}>
      <div className="card w-full max-w-lg p-6 space-y-4" onClick={(e) => e.stopPropagation()}>
        <h3 className="text-lg font-semibold">编辑直播间</h3>

        <div>
          <label className="text-xs text-ink-3">封面图</label>
          <div className="mt-1 flex items-center gap-3">
            <div className="w-28 h-16 rounded-lg bg-bg-3 overflow-hidden shrink-0 flex items-center justify-center">
              {coverUrl ? (
                <img src={coverUrl} className="w-full h-full object-cover" alt="" />
              ) : (
                <Tv size={20} className="text-ink-4" />
              )}
            </div>
            <input type="file" accept="image/*" ref={fileRef} onChange={pickCover} className="hidden" />
            <button
              onClick={() => fileRef.current?.click()}
              disabled={uploading}
              className="btn-secondary text-xs"
            >
              {uploading ? <Loader2 size={14} className="animate-spin" /> : <Upload size={14} />}
              {coverUrl ? '更换封面' : '上传封面'}
            </button>
            {coverUrl && (
              <button onClick={() => setCoverUrl('')} className="btn-icon text-ink-3" title="移除封面">
                <Trash2 size={14} />
              </button>
            )}
          </div>
        </div>

        <div>
          <label className="text-xs text-ink-3">标题</label>
          <input className="input mt-1" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={128} />
        </div>

        <div>
          <label className="text-xs text-ink-3">分类</label>
          <input className="input mt-1" value={category} onChange={(e) => setCategory(e.target.value)} placeholder="科技 / 游戏 / 闲聊" maxLength={32} />
        </div>

        <div>
          <label className="text-xs text-ink-3 flex items-center gap-1">
            <Calendar size={11} /> 预告开播时间 <span className="text-ink-4">（可选）</span>
          </label>
          <div className="mt-1 flex items-center gap-2">
            <input
              type="datetime-local"
              className="input flex-1"
              value={schedule}
              onChange={(e) => setSchedule(e.target.value)}
              min={new Date().toISOString().slice(0, 16)}
            />
            {schedule && (
              <button onClick={() => setSchedule('')} className="btn-icon text-ink-3" title="清除预告">
                <Trash2 size={14} />
              </button>
            )}
          </div>
          <div className="text-[11px] text-ink-4 mt-1">
            设了预告时间后，会在「广场」上方显示，关注的用户也能收到提醒。
          </div>
        </div>

        <div className="flex gap-2 justify-end pt-2 border-t border-bg-5/40">
          <button onClick={onClose} className="btn-secondary">取消</button>
          <button onClick={save} disabled={saving || !title.trim()} className="btn-primary">
            {saving ? <Loader2 size={14} className="animate-spin" /> : null} 保存
          </button>
        </div>
      </div>
    </div>
  );
}
