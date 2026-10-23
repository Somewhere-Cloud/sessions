import type { SessionInfo, SessionObservations } from '../types';

const WORKING_SOURCES: Record<string, string> = {
  'provider-stream': 'from the provider’s own events',
  'rollout-file': 'from Codex’s rollout file',
  'terminal-heuristic': 'read from the terminal screen',
  'byte-activity': 'from recent terminal output only',
  'provider-retry': 'a provider retry is pending'
};

/** A daemon time in epoch milliseconds, with its age when it is in the past, or 'unknown'. */
export function observedTime(at: number | null | undefined, now: number): string {
  if (at === null || at === undefined) return 'unknown';
  const date = new Date(at);
  if (!Number.isFinite(date.getTime())) return 'unknown';
  const stamp = date.toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC');
  const elapsed = now - at;
  return elapsed >= 0 ? `${stamp} (${elapsedText(elapsed)} ago)` : stamp;
}

function elapsedText(ms: number): string {
  const minutes = Math.floor(ms / 60000);
  if (minutes < 1) return `${Math.floor(ms / 1000)}s`;
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

function turnNote(turn: NonNullable<SessionObservations['turn']>, now: number): string {
  if (turn.open === null) return 'No turn boundary observed.';
  if (!turn.open) return `Turn ended ${observedTime(turn.ended_at, now)} by ${turn.ended_by ?? 'unknown'}.`;
  const started = `Turn open since ${observedTime(turn.started_at, now)}, started by ${turn.started_by ?? 'unknown'}`;
  if (turn.started_by === 'sessions-dispatch' && !turn.provider_confirmed_at) return `${started}; not confirmed by the provider.`;
  return `${started}.`;
}

function toolsNote(tools: NonNullable<SessionObservations['tools']>, now: number): string {
  const plural = tools.open_count === 1 ? '' : 's';
  const count = tools.open_count_exact
    ? `${tools.open_count} tool call${plural} open.`
    : `${tools.open_count} tool start${plural} without a recorded end (partial history; current open count unknown).`;
  const status = tools.last_end_status ? ` (${tools.last_end_status})` : '';
  return `${count} Last tool end ${observedTime(tools.last_ended_at, now)}${status}.`;
}

function hooksNote(hooks: NonNullable<SessionObservations['hooks']>, now: number): string {
  if (hooks.coverage === 'unavailable') return `Claude hooks unavailable (${hooks.unavailable_reason ?? 'unknown'}).`;
  const last = hooks.last_event;
  const lastText = last
    ? `Last Claude hook ${last.name}${last.detail ? ` (${last.detail})` : ''} observed ${observedTime(last.observed_at, now)}.`
    : 'Claude hooks configured; no hook event received yet.';
  return hooks.gaps.length > 0 ? `${lastText} Hook history incomplete: ${hooks.gaps.join(', ')}.` : lastText;
}

/**
 * Where a session's state words come from, and the newest facts behind them,
 * in plain sentences, or '' when the daemon did not say. It restates the
 * daemon's observations with their times; it never reclassifies them, applies
 * no staleness threshold, and names an observer that is not following.
 */
export function observationNote(session: SessionInfo, now: number = Date.now()): string {
  const observations = session.observations;
  if (!observations) return '';
  const parts: string[] = [];
  const source = observations.working ? WORKING_SOURCES[observations.working.source] : undefined;
  if (source) parts.push(`Working state ${source}.`);
  if (observations.turn) parts.push(turnNote(observations.turn, now));
  if (observations.last) {
    parts.push(`Last provider record observed ${observedTime(observations.last.provider_record_observed_at, now)}; its own time ${observedTime(observations.last.provider_record_event_at, now)}.`);
  }
  if (observations.tools) parts.push(toolsNote(observations.tools, now));
  if (observations.hooks) parts.push(hooksNote(observations.hooks, now));
  for (const observer of observations.observers) {
    if (observer.state !== 'following') parts.push(`${observer.name} ${observer.state}${observer.detail ? `: ${observer.detail}` : ''}.`);
  }
  return parts.join(' ');
}
