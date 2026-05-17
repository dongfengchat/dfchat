import type { WSEvent } from '@/types';

type Handler = (ev: WSEvent) => void;
type OpenHandler = () => void;

const API_BASE = import.meta.env.VITE_API_BASE ?? 'http://localhost:8080';

// Close codes the server uses (see realtime/handler.go). Mirrored here so
// we can react specifically rather than treating every close as transient.
const CLOSE_TOKEN_EXPIRED = 4401;
const CLOSE_TOO_MANY_CONNS = 4429;

export class WSClient {
  private ws: WebSocket | null = null;
  private handlers = new Set<Handler>();
  private openHandlers = new Set<OpenHandler>();
  private reconnectAttempts = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private manuallyClosed = false;
  private refreshInFlight: Promise<string | null> | null = null;
  // Application-layer heartbeat (separate from WS protocol ping/pong, which
  // some intermediate proxies/NAT boxes don't reset the idle timer on).
  // We send {type:"ping"} every 20s and expect "pong" back; if we miss
  // two consecutive expected pongs we treat the socket as dead.
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private lastPongAt = 0;

  connect(_token?: string) {
    // The token argument is accepted for backwards compat but ignored —
    // we always read the latest accessToken from localStorage at the
    // moment of connect, so a REST-interceptor refresh that landed in
    // between is picked up automatically.
    this.manuallyClosed = false;
    this.open();
  }

  private currentToken(): string | null {
    try {
      return localStorage.getItem('accessToken');
    } catch {
      return null;
    }
  }

  private wsURL(token: string): string {
    const httpBase = API_BASE;
    const wsBase = httpBase.replace(/^http/, 'ws');
    return `${wsBase}/ws?token=${encodeURIComponent(token)}`;
  }

  private open() {
    const token = this.currentToken();
    if (!token) return;
    try {
      this.ws = new WebSocket(this.wsURL(token));
    } catch (e) {
      this.scheduleReconnect();
      return;
    }
    this.ws.onopen = () => {
      this.reconnectAttempts = 0;
      this.lastPongAt = Date.now();
      this.startHeartbeat();
      this.openHandlers.forEach((h) => h());
    };
    this.ws.onmessage = (ev) => {
      try {
        const data = JSON.parse(ev.data) as WSEvent;
        if (data.type === 'pong') {
          this.lastPongAt = Date.now();
          return;
        }
        this.handlers.forEach((h) => h(data));
      } catch {
        // ignore malformed
      }
    };
    this.ws.onclose = (ev) => {
      this.ws = null;
      this.stopHeartbeat();
      if (this.manuallyClosed) return;
      // Server-side reasons we should not just blindly back-off:
      //   4401 — token expired. Refresh first, then reconnect immediately.
      //   4429 — too many connections for this account. Back off hard.
      // Anything else is a normal disconnect → exponential reconnect.
      if (ev.code === CLOSE_TOKEN_EXPIRED) {
        this.handleTokenExpired();
        return;
      }
      if (ev.code === CLOSE_TOO_MANY_CONNS) {
        // Punish with a 60s wait so a duplicate-instance accident
        // doesn't burn through our reconnect budget.
        this.reconnectAttempts = 6; // 2^6 * 1000 = 64s
        this.scheduleReconnect();
        return;
      }
      this.scheduleReconnect();
    };
    this.ws.onerror = () => {
      // close will fire next; let it handle reconnect
    };
  }

  // handleTokenExpired runs the refresh dance once (single-flight) so
  // a brief flurry of close-4401 events doesn't hammer /auth/refresh.
  // On success we reset the backoff and reconnect immediately; on
  // failure we let the REST interceptor bounce the user to /login.
  private handleTokenExpired() {
    if (!this.refreshInFlight) {
      this.refreshInFlight = (async () => {
        try {
          const refreshToken = localStorage.getItem('refreshToken');
          if (!refreshToken) return null;
          const res = await fetch(`${API_BASE}/api/v1/auth/refresh`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ refreshToken }),
          });
          if (!res.ok) return null;
          const data = await res.json();
          if (!data?.accessToken || !data?.refreshToken) return null;
          localStorage.setItem('accessToken', data.accessToken);
          localStorage.setItem('refreshToken', data.refreshToken);
          return data.accessToken as string;
        } catch {
          return null;
        } finally {
          setTimeout(() => (this.refreshInFlight = null), 0);
        }
      })();
    }
    this.refreshInFlight.then((newAccess) => {
      if (this.manuallyClosed) return;
      if (newAccess) {
        this.reconnectAttempts = 0;
        this.open();
      } else {
        // Refresh failed — leave it to the REST interceptor to bounce
        // to /login on the next 401, instead of looping on WS forever.
        this.manuallyClosed = true;
      }
    });
  }

  private startHeartbeat() {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
      // 50s without a pong → consider socket dead. With our 20s ping cadence
      // that means we missed two pongs in a row; the socket might look
      // "open" but the bytes aren't getting through (idle NAT, sleeping
      // laptop, hostile proxy). Force-close → onclose → exponential reconnect.
      if (this.lastPongAt && Date.now() - this.lastPongAt > 50000) {
        try { this.ws.close(); } catch { /* ignore */ }
        return;
      }
      const env = { msgId: cryptoId(), type: 'ping', payload: {}, ts: Date.now() };
      try { this.ws.send(JSON.stringify(env)); } catch { /* close will fire */ }
    }, 20000);
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private scheduleReconnect() {
    if (this.reconnectTimer) return;
    const delay = Math.min(1000 * 2 ** this.reconnectAttempts, 30000);
    this.reconnectAttempts++;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.open();
    }, delay);
  }

  on(handler: Handler): () => void {
    this.handlers.add(handler);
    return () => this.handlers.delete(handler);
  }

  /** Returns true if the message went onto the socket, false if the
   *  socket isn't open (caller can show "reconnecting" UI). */
  send(type: string, payload: unknown): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      // Nudge the reconnect loop so we don't sit idle if the close handler
      // somehow missed the disconnect (e.g. machine sleep / network change).
      if (!this.manuallyClosed && this.currentToken()) this.scheduleReconnect();
      return false;
    }
    const env = { msgId: cryptoId(), type, payload, ts: Date.now() };
    this.ws.send(JSON.stringify(env));
    return true;
  }

  isOpen(): boolean {
    return !!this.ws && this.ws.readyState === WebSocket.OPEN;
  }

  onOpen(handler: OpenHandler): () => void {
    this.openHandlers.add(handler);
    return () => this.openHandlers.delete(handler);
  }

  close() {
    this.manuallyClosed = true;
    this.stopHeartbeat();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.handlers.clear();
    this.openHandlers.clear();
  }
}

function cryptoId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `m_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
}

export const wsClient = new WSClient();
