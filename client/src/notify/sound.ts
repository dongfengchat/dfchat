// Notification sound — synthesized via Web Audio so we don't ship an mp3
// asset (saves ~30 KB and avoids loader plumbing). Two distinct chimes:
//   - "normal":  880 Hz, 0.3 s, soft  — for regular messages
//   - "mention": 1320 Hz two-tone, 0.5 s, brighter — when you're @-mentioned
//
// The audio context is lazily created on first call to satisfy browser
// autoplay rules (a user-gesture path triggered the WS connect that
// triggered this).

let ctx: AudioContext | null = null;
function audioCtx(): AudioContext | null {
  if (ctx) return ctx;
  const AC = (window as any).AudioContext || (window as any).webkitAudioContext;
  if (!AC) return null;
  ctx = new AC();
  return ctx;
}

function tone(freq: number, durationMs: number, delayMs = 0, peakGain = 0.12) {
  const c = audioCtx();
  if (!c) return;
  const t0 = c.currentTime + delayMs / 1000;
  const t1 = t0 + durationMs / 1000;
  const osc = c.createOscillator();
  const gain = c.createGain();
  osc.connect(gain).connect(c.destination);
  osc.type = 'sine';
  osc.frequency.setValueAtTime(freq, t0);
  // Quick attack + exponential decay → "ding" feel.
  gain.gain.setValueAtTime(0, t0);
  gain.gain.linearRampToValueAtTime(peakGain, t0 + 0.01);
  gain.gain.exponentialRampToValueAtTime(0.0001, t1);
  osc.start(t0);
  osc.stop(t1 + 0.01);
}

/** Soft chime for a regular incoming message. */
export function playNotifyChime() {
  tone(880, 300);
}

/** Brighter two-note chime when the user is @-mentioned. */
export function playMentionChime() {
  tone(1320, 180, 0);
  tone(990, 240, 200, 0.10);
}

/** Whether notification sound is on. localStorage-backed, so users can mute. */
export function isSoundEnabled(): boolean {
  return localStorage.getItem('notify.sound') !== '0';
}

export function setSoundEnabled(on: boolean) {
  localStorage.setItem('notify.sound', on ? '1' : '0');
}
