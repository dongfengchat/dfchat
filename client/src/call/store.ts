// Voice + video call state machine. One concurrent call. Signaling rides
// on the existing WebSocket; the server is a pure relay (call.* events).
//
// Video and screen-sharing share the same RTCRtpSender — we swap the
// outgoing track via sender.replaceTrack() so the negotiated SDP stays
// stable and the peer doesn't have to renegotiate.

import { create } from 'zustand';
import { wsClient } from '@/ws/client';
import { api } from '@/api/client';

export type CallKind = 'audio' | 'video';

export type CallState =
  | { phase: 'idle' }
  | { phase: 'inviting'; peerId: string; peerName: string; kind: CallKind }
  | { phase: 'incoming'; peerId: string; peerName: string; kind: CallKind }
  | { phase: 'connected'; peerId: string; peerName: string; kind: CallKind;
      muted: boolean; cameraOff: boolean; screenSharing: boolean;
      localStream: MediaStream | null; remoteStream: MediaStream | null;
      startedAt: number }
  | { phase: 'ended'; reason: string };

interface CallStore {
  state: CallState;
  startCall: (peerId: string, peerName: string, kind: CallKind) => Promise<void>;
  acceptCall: () => Promise<void>;
  rejectCall: () => void;
  endCall: () => void;
  toggleMute: () => void;
  toggleCamera: () => Promise<void>;
  toggleScreenShare: () => Promise<void>;
  end: () => void;
  _setState: (s: CallState) => void;
}

let pc: RTCPeerConnection | null = null;
let localStream: MediaStream | null = null;
let remoteStream: MediaStream | null = null;
// Camera video track kept aside while screen-sharing so we can restore it.
let cameraVideoTrack: MediaStreamTrack | null = null;
let screenStream: MediaStream | null = null;
const pendingCandidates: RTCIceCandidateInit[] = [];

// Static fallback — used only if /api/v1/turn/credentials fails or the
// backend hasn't been configured with a TURN secret yet. Cone/full-cone
// NAT users get through with STUN-only, symmetric NAT users won't.
const FALLBACK_ICE_SERVERS: RTCIceServer[] = [
  { urls: ['stun:stun.l.google.com:19302', 'stun:stun1.l.google.com:19302'] },
];

interface TurnCredentialsResponse {
  iceServers: RTCIceServer[];
  ttl: number;
}

// Cache the iceServers per call session. Each TURN credential lives 30 min
// which is comfortably longer than any reasonable call, so we just fetch
// fresh credentials at call setup and stop worrying about rotation.
async function fetchIceServers(): Promise<RTCIceServer[]> {
  try {
    const res = await api.get<TurnCredentialsResponse>('/api/v1/turn/credentials');
    if (Array.isArray(res.data?.iceServers) && res.data.iceServers.length > 0) {
      return res.data.iceServers;
    }
  } catch {
    // network / 401 / etc — fall through to static config
  }
  return FALLBACK_ICE_SERVERS;
}

function findVideoSender(): RTCRtpSender | null {
  if (!pc) return null;
  return pc.getSenders().find((s) => s.track && s.track.kind === 'video') ?? null;
}

function teardown() {
  if (pc) {
    try { pc.close(); } catch { /* ignore */ }
    pc = null;
  }
  if (localStream) {
    localStream.getTracks().forEach((t) => t.stop());
    localStream = null;
  }
  if (screenStream) {
    screenStream.getTracks().forEach((t) => t.stop());
    screenStream = null;
  }
  if (cameraVideoTrack) {
    try { cameraVideoTrack.stop(); } catch { /* ignore */ }
    cameraVideoTrack = null;
  }
  remoteStream = null;
  pendingCandidates.length = 0;
}

async function newPeerConnection(peerId: string, kind: CallKind): Promise<RTCPeerConnection> {
  const iceServers = await fetchIceServers();
  pc = new RTCPeerConnection({ iceServers });
  const constraints: MediaStreamConstraints =
    kind === 'video' ? { audio: true, video: { width: 1280, height: 720 } } : { audio: true };
  localStream = await navigator.mediaDevices.getUserMedia(constraints);
  localStream.getTracks().forEach((t) => pc!.addTrack(t, localStream!));
  if (kind === 'video') {
    cameraVideoTrack = localStream.getVideoTracks()[0] ?? null;
  }

  remoteStream = new MediaStream();
  pc.ontrack = (e) => {
    e.streams[0]?.getTracks().forEach((t) => {
      // Avoid duplicate track adds on renegotiation.
      if (!remoteStream!.getTracks().find((x) => x.id === t.id)) {
        remoteStream!.addTrack(t);
      }
    });
    // Trigger a state refresh so the UI re-renders with the now-populated stream.
    const s = useCallStore.getState().state;
    if (s.phase === 'connected') {
      useCallStore.setState({ state: { ...s, remoteStream } });
    }
  };
  pc.onicecandidate = (e) => {
    if (e.candidate) {
      wsClient.send('call.signal', { to: peerId, kind: 'ice', candidate: e.candidate });
    }
  };
  pc.onconnectionstatechange = () => {
    if (!pc) return;
    if (pc.connectionState === 'failed' || pc.connectionState === 'disconnected') {
      useCallStore.getState()._setState({ phase: 'ended', reason: '连接断开' });
      teardown();
    }
  };
  return pc;
}

async function flushPendingCandidates() {
  if (!pc) return;
  for (const c of pendingCandidates.splice(0)) {
    try { await pc.addIceCandidate(c); } catch { /* ignore */ }
  }
}

export const useCallStore = create<CallStore>((set, get) => ({
  state: { phase: 'idle' },

  startCall: async (peerId, peerName, kind) => {
    teardown();
    set({ state: { phase: 'inviting', peerId, peerName, kind } });
    try {
      await newPeerConnection(peerId, kind);
      const offer = await pc!.createOffer({
        offerToReceiveAudio: true,
        offerToReceiveVideo: kind === 'video',
      });
      await pc!.setLocalDescription(offer);
      wsClient.send('call.invite', { to: peerId, kind, sdp: offer });
    } catch (e) {
      teardown();
      set({ state: { phase: 'ended', reason: '无法启动通话（设备权限？）' } });
    }
  },

  acceptCall: async () => {
    const s = get().state;
    if (s.phase !== 'incoming') return;
    try {
      await flushPendingCandidates();
      const answer = await pc!.createAnswer();
      await pc!.setLocalDescription(answer);
      wsClient.send('call.accept', { to: s.peerId, sdp: answer });
      set({
        state: {
          phase: 'connected', peerId: s.peerId, peerName: s.peerName, kind: s.kind,
          muted: false, cameraOff: false, screenSharing: false,
          localStream, remoteStream, startedAt: Date.now(),
        },
      });
    } catch (e) {
      teardown();
      set({ state: { phase: 'ended', reason: '应答失败' } });
    }
  },

  rejectCall: () => {
    const s = get().state;
    if (s.phase === 'incoming') {
      wsClient.send('call.reject', { to: s.peerId });
    }
    teardown();
    set({ state: { phase: 'idle' } });
  },

  endCall: () => {
    const s = get().state;
    if (s.phase === 'connected' || s.phase === 'inviting') {
      wsClient.send('call.end', { to: s.peerId });
    }
    teardown();
    set({ state: { phase: 'idle' } });
  },

  toggleMute: () => {
    const s = get().state;
    if (s.phase !== 'connected' || !localStream) return;
    const next = !s.muted;
    localStream.getAudioTracks().forEach((t) => (t.enabled = !next));
    set({ state: { ...s, muted: next } });
  },

  toggleCamera: async () => {
    const s = get().state;
    if (s.phase !== 'connected' || s.kind !== 'video' || !localStream) return;
    const tracks = localStream.getVideoTracks();
    if (tracks.length === 0) return;
    const t = tracks[0];
    t.enabled = !t.enabled;
    set({ state: { ...s, cameraOff: !t.enabled } });
  },

  toggleScreenShare: async () => {
    const s = get().state;
    if (s.phase !== 'connected' || !pc) return;
    const sender = findVideoSender();
    if (!sender) {
      // Audio-only call — we'd need to renegotiate to add a video track. Skip
      // for MVP; users should start a video call to use screen share.
      return;
    }
    if (!s.screenSharing) {
      // Start sharing.
      try {
        const stream = await (navigator.mediaDevices as MediaDevices & {
          getDisplayMedia: (c: MediaStreamConstraints) => Promise<MediaStream>;
        }).getDisplayMedia({ video: true, audio: false });
        screenStream = stream;
        const screenTrack = stream.getVideoTracks()[0];
        if (!screenTrack) return;
        // Auto-stop when user clicks the OS-level stop-sharing button.
        screenTrack.onended = () => {
          // Replay toggle to restore camera.
          useCallStore.getState().toggleScreenShare();
        };
        await sender.replaceTrack(screenTrack);
        set({ state: { ...s, screenSharing: true } });
      } catch { /* user cancelled the share picker */ }
    } else {
      // Stop sharing — swap camera track back in.
      if (screenStream) {
        screenStream.getTracks().forEach((t) => t.stop());
        screenStream = null;
      }
      if (cameraVideoTrack) {
        await sender.replaceTrack(cameraVideoTrack);
      }
      set({ state: { ...s, screenSharing: false } });
    }
  },

  end: () => {
    teardown();
    set({ state: { phase: 'idle' } });
  },

  _setState: (state) => set({ state }),
}));

// ---- inbound handling ----

interface CallPayload {
  from?: string;
  to?: string;
  sdp?: RTCSessionDescriptionInit;
  candidate?: RTCIceCandidateInit;
  kind?: string; // doubles as 'audio'|'video' for invite, 'ice' for signal
}

export async function handleIncomingCallEvent(type: string, payload: CallPayload) {
  const store = useCallStore.getState();
  const fromId = payload.from || '';
  switch (type) {
    case 'call.invite': {
      if (store.state.phase !== 'idle') {
        wsClient.send('call.reject', { to: fromId });
        return;
      }
      const kind: CallKind = payload.kind === 'video' ? 'video' : 'audio';
      await newPeerConnection(fromId, kind);
      if (payload.sdp) {
        await pc!.setRemoteDescription(payload.sdp);
      }
      useCallStore.setState({
        state: { phase: 'incoming', peerId: fromId, peerName: fromId, kind },
      });
      break;
    }
    case 'call.accept': {
      if (!pc || !payload.sdp) return;
      await pc.setRemoteDescription(payload.sdp);
      await flushPendingCandidates();
      const s = store.state;
      if (s.phase === 'inviting') {
        useCallStore.setState({
          state: {
            phase: 'connected', peerId: s.peerId, peerName: s.peerName, kind: s.kind,
            muted: false, cameraOff: false, screenSharing: false,
            localStream, remoteStream, startedAt: Date.now(),
          },
        });
      }
      break;
    }
    case 'call.reject': {
      teardown();
      useCallStore.setState({ state: { phase: 'ended', reason: '对方拒绝' } });
      break;
    }
    case 'call.signal': {
      if (payload.kind === 'ice' && payload.candidate) {
        if (pc && pc.remoteDescription) {
          try { await pc.addIceCandidate(payload.candidate); } catch { /* ignore */ }
        } else {
          pendingCandidates.push(payload.candidate);
        }
      }
      break;
    }
    case 'call.end': {
      teardown();
      useCallStore.setState({ state: { phase: 'ended', reason: '通话已结束' } });
      break;
    }
  }
}
