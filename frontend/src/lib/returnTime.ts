// Times a person reads: when a set-aside session comes back, when a limited
// session resumes. Shared by the snooze presets and status labels so both
// surfaces name the same moment the same way.

function sameDay(left: Date, right: Date): boolean {
  return left.getFullYear() === right.getFullYear()
    && left.getMonth() === right.getMonth()
    && left.getDate() === right.getDate();
}

function clock(at: Date): string {
  return at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

/** "14:05", "tomorrow 09:00", "Wed 09:00", or "Dec 4 09:00". */
export function returnTimeLabel(ms: number, now: Date = new Date()): string {
  const at = new Date(ms);
  const tomorrow = new Date(now);
  tomorrow.setDate(now.getDate() + 1);
  if (sameDay(at, now)) return clock(at);
  if (sameDay(at, tomorrow)) return `tomorrow ${clock(at)}`;
  if (ms - now.getTime() < 6 * 24 * 3_600_000) {
    return `${at.toLocaleDateString([], { weekday: 'short' })} ${clock(at)}`;
  }
  return `${at.toLocaleDateString([], { month: 'short', day: 'numeric' })} ${clock(at)}`;
}

export interface SnoozePreset {
  label: string;
  /** Unix ms, as `sessions aside ID --until` sends it. */
  until: number;
}

function atMorning(day: Date): number {
  const morning = new Date(day);
  morning.setHours(9, 0, 0, 0);
  return morning.getTime();
}

/** The compact preset list: 1 hour, 3 hours, tomorrow morning, next week. */
export function snoozePresets(now: Date = new Date()): SnoozePreset[] {
  const tomorrow = new Date(now);
  tomorrow.setDate(now.getDate() + 1);
  const nextMonday = new Date(now);
  nextMonday.setDate(now.getDate() + (((8 - now.getDay()) % 7) || 7));
  return [
    { label: '1 hour', until: now.getTime() + 3_600_000 },
    { label: '3 hours', until: now.getTime() + 3 * 3_600_000 },
    { label: 'Tomorrow morning', until: atMorning(tomorrow) },
    { label: 'Next week', until: atMorning(nextMonday) }
  ];
}

/** A rate-limited session Sessions will resume on its own. */
export function resumesAfterLimit(session: {
  failureKind?: string;
  retry?: unknown;
  limitRecovery?: { state: string; resumeAt?: number } | null;
}): number | null {
  const plan = session.limitRecovery;
  if (session.failureKind !== 'rate-limited' || session.retry || plan?.state !== 'scheduled' || !plan.resumeAt) return null;
  return plan.resumeAt;
}
