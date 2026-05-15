import { useEffect, useRef, useState } from 'react';
import {
  Mic,
  MicOff,
  Monitor,
  MonitorOff,
  Phone,
  PhoneOff,
  Video,
  VideoOff,
} from 'lucide-react';
import { useCallStore } from '@/call/store';
import { useChatStore } from '@/store/chatStore';
import Avatar from './ui/Avatar';

function durationLabel(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(s / 60);
  const r = s % 60;
  return `${m.toString().padStart(2, '0')}:${r.toString().padStart(2, '0')}`;
}

export default function CallOverlay() {
  const state = useCallStore((s) => s.state);
  const friends = useChatStore((s) => s.friends);
  const localVideoRef = useRef<HTMLVideoElement>(null);
  const remoteVideoRef = useRef<HTMLVideoElement>(null);
  const remoteAudioRef = useRef<HTMLAudioElement>(null);
  const [now, setNow] = useState(() => Date.now());

  // 1Hz ticker so the duration label updates.
  useEffect(() => {
    if (state.phase !== 'connected') return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [state.phase]);

  // Attach media streams to their <video>/<audio> elements whenever the
  // call state's streams change.
  useEffect(() => {
    if (state.phase !== 'connected') return;
    if (state.kind === 'video' && state.localStream && localVideoRef.current) {
      localVideoRef.current.srcObject = state.localStream;
    }
    if (state.kind === 'video' && state.remoteStream && remoteVideoRef.current) {
      remoteVideoRef.current.srcObject = state.remoteStream;
    }
    if (state.kind === 'audio' && state.remoteStream && remoteAudioRef.current) {
      remoteAudioRef.current.srcObject = state.remoteStream;
    }
  }, [
    state.phase,
    state.phase === 'connected' ? state.kind : null,
    state.phase === 'connected' ? state.localStream : null,
    state.phase === 'connected' ? state.remoteStream : null,
  ]);

  useEffect(() => {
    if (state.phase !== 'ended') return;
    const t = setTimeout(() => useCallStore.getState().end(), 1800);
    return () => clearTimeout(t);
  }, [state.phase]);

  if (state.phase === 'idle') return null;

  let peerName = '';
  if (state.phase !== 'ended') {
    const f = friends.find((x) => x.id === state.peerId);
    peerName = f ? (f.nickname || f.username) : state.peerName;
  }

  const isVideo =
    state.phase === 'connected' ? state.kind === 'video' :
    state.phase === 'inviting' || state.phase === 'incoming' ? state.kind === 'video' :
    false;

  // Connected video call → full-screen layout. Everything else → corner card.
  if (state.phase === 'connected' && isVideo) {
    const { muted, cameraOff, screenSharing, startedAt } = state;
    return (
      <div className="fixed inset-0 z-40 bg-black/95 flex flex-col anim-fade">
        <div className="px-5 py-3 flex items-center gap-3 bg-bg-2/60 backdrop-blur">
          <Avatar name={peerName || '?'} size={36} />
          <div>
            <div className="font-medium text-ink-1">{peerName}</div>
            <div className="text-xs text-ink-3">
              {screenSharing ? '正在共享屏幕' : '视频通话'} · {durationLabel(now - startedAt)}
            </div>
          </div>
        </div>

        <div className="flex-1 relative flex items-center justify-center">
          <video
            ref={remoteVideoRef}
            autoPlay
            playsInline
            className="w-full h-full object-contain bg-black"
          />
          {/* PiP — local preview in lower-right */}
          <div className="absolute bottom-5 right-5 w-48 aspect-video rounded-lg overflow-hidden shadow-pop bg-black border border-bg-5/40">
            <video
              ref={localVideoRef}
              autoPlay
              playsInline
              muted
              className={`w-full h-full object-cover ${cameraOff ? 'opacity-0' : ''}`}
            />
            {cameraOff && (
              <div className="absolute inset-0 flex items-center justify-center text-ink-3 text-xs">
                摄像头已关
              </div>
            )}
          </div>
        </div>

        <CallControls
          isVideo
          muted={muted}
          cameraOff={cameraOff}
          screenSharing={screenSharing}
        />
      </div>
    );
  }

  // Corner card for incoming/inviting/audio-connected/ended.
  return (
    <div className="fixed top-5 right-5 z-40 w-80 card shadow-pop p-4 anim-slide">
      <audio ref={remoteAudioRef} autoPlay />
      <div className="flex items-center gap-3">
        <Avatar name={peerName || '?'} size={42} />
        <div className="min-w-0 flex-1">
          <div className="font-medium truncate text-ink-1">{peerName || '通话'}</div>
          <div className="text-xs text-ink-3">
            {state.phase === 'inviting' && (isVideo ? '正在发起视频通话…' : '正在发起语音通话…')}
            {state.phase === 'incoming' && (isVideo ? '视频来电' : '语音来电')}
            {state.phase === 'connected' && (
              <>
                {state.muted ? '已静音' : '通话中'} · {durationLabel(now - state.startedAt)}
              </>
            )}
            {state.phase === 'ended' && state.reason}
          </div>
        </div>
      </div>

      <CallControls
        isVideo={false}
        muted={state.phase === 'connected' ? state.muted : false}
        cameraOff={false}
        screenSharing={false}
      />
    </div>
  );
}

function CallControls({
  isVideo,
  muted,
  cameraOff,
  screenSharing,
}: {
  isVideo: boolean;
  muted: boolean;
  cameraOff: boolean;
  screenSharing: boolean;
}) {
  const state = useCallStore((s) => s.state);
  const { acceptCall, rejectCall, endCall, toggleMute, toggleCamera, toggleScreenShare } = useCallStore.getState();

  if (state.phase === 'inviting') {
    return (
      <button onClick={endCall} className="btn-danger w-full mt-3">
        <PhoneOff size={16} /> 取消
      </button>
    );
  }
  if (state.phase === 'incoming') {
    return (
      <div className="flex gap-2 mt-3">
        <button onClick={rejectCall} className="btn-secondary flex-1">
          <PhoneOff size={16} /> 拒绝
        </button>
        <button
          onClick={acceptCall}
          className="flex-1 inline-flex items-center justify-center gap-1.5 px-3 py-2 rounded-lg bg-accent-green hover:opacity-90 text-white text-sm font-medium"
        >
          <Phone size={16} /> 接听
        </button>
      </div>
    );
  }
  if (state.phase === 'connected') {
    if (isVideo) {
      return (
        <div className="bg-bg-2/80 backdrop-blur py-4 flex items-center justify-center gap-3">
          <RoundBtn label={muted ? '取消静音' : '静音'} active={muted} onClick={toggleMute}>
            {muted ? <MicOff size={18} /> : <Mic size={18} />}
          </RoundBtn>
          <RoundBtn label={cameraOff ? '开启摄像头' : '关闭摄像头'} active={cameraOff} onClick={() => void toggleCamera()}>
            {cameraOff ? <VideoOff size={18} /> : <Video size={18} />}
          </RoundBtn>
          <RoundBtn label={screenSharing ? '停止共享' : '共享屏幕'} active={screenSharing} accent={screenSharing} onClick={() => void toggleScreenShare()}>
            {screenSharing ? <MonitorOff size={18} /> : <Monitor size={18} />}
          </RoundBtn>
          <button
            onClick={endCall}
            className="w-14 h-14 rounded-full bg-accent-red hover:opacity-90 text-white inline-flex items-center justify-center"
            title="挂断"
          >
            <PhoneOff size={22} />
          </button>
        </div>
      );
    }
    return (
      <div className="flex gap-2 mt-3">
        <button onClick={toggleMute} className="btn-secondary flex-1">
          {muted ? <MicOff size={16} /> : <Mic size={16} />}
          {muted ? '取消静音' : '静音'}
        </button>
        <button onClick={endCall} className="btn-danger flex-1">
          <PhoneOff size={16} /> 挂断
        </button>
      </div>
    );
  }
  return null;
}

function RoundBtn({
  label,
  active,
  accent,
  onClick,
  children,
}: {
  label: string;
  active?: boolean;
  accent?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  const base = 'w-12 h-12 rounded-full inline-flex items-center justify-center transition-colors';
  const cls = accent
    ? `${base} bg-brand-500 hover:bg-brand-600 text-white`
    : active
    ? `${base} bg-bg-4 text-ink-1`
    : `${base} bg-bg-3 hover:bg-bg-4 text-ink-1`;
  return (
    <button onClick={onClick} className={cls} title={label} aria-label={label}>
      {children}
    </button>
  );
}
