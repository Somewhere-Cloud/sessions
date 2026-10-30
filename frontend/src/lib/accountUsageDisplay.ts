import type { AccountUsageBucket, AccountUsageWindow } from '../api/sessionsd';

export function relativeTime(at: number, now = Date.now()): string {
  const minutes = Math.round((now - at) / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours} h ago`;
  return new Date(at).toLocaleDateString();
}

export function windowName(window: AccountUsageWindow): string {
  const minutes = window.window_minutes;
  if (!minutes) return window.kind === 'secondary' ? 'Longer limit' : 'Current limit';
  if (minutes === 10_080) return 'Weekly limit';
  if (minutes === 1_440) return 'Daily limit';
  if (minutes % 1_440 === 0) return `${minutes / 1_440}-day limit`;
  if (minutes % 60 === 0) return `${minutes / 60}-hour limit`;
  return `${minutes}-minute limit`;
}

export function resetText(at: number | undefined): string {
  if (!at) return 'reset time not reported';
  if (at <= Date.now()) return 'reported reset time has passed';
  const reset = new Date(at);
  const soon = at - Date.now() < 24 * 60 * 60_000;
  return `resets ${soon
    ? reset.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
    : reset.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })}`;
}

export function bucketName(bucket: AccountUsageBucket, many: boolean): string | null {
  if (bucket.limit_name) return bucket.limit_name;
  if (!many) return null;
  if (bucket.limit_id === 'claude') return 'All models';
  return bucket.limit_id === 'codex' ? 'Codex' : bucket.limit_id ?? 'Other limit';
}
