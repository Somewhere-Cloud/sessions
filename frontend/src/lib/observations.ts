import type { SessionInfo } from '../types';

const WORKING_SOURCES: Record<string, string> = {
  'provider-stream': 'from the provider’s own events',
  'rollout-file': 'from Codex’s rollout file',
  'terminal-heuristic': 'read from the terminal screen',
  'byte-activity': 'from recent terminal output only',
  'provider-retry': 'a provider retry is pending'
};

/**
 * Where a session's state words come from, in one plain sentence, or '' when
 * the daemon did not say. It restates the daemon's observations; it never
 * reclassifies them, and it names an observer that is not following.
 */
export function observationNote(session: SessionInfo): string {
  const observations = session.observations;
  if (!observations) return '';
  const parts: string[] = [];
  const source = observations.working ? WORKING_SOURCES[observations.working.source] : undefined;
  if (source) parts.push(`Working state ${source}.`);
  if (observations.turn && observations.turn.open === null) parts.push('No turn boundary observed.');
  if (observations.tools && observations.tools.open_count > 0) {
    parts.push(`${observations.tools.open_count} tool call${observations.tools.open_count === 1 ? '' : 's'} open${observations.tools.history_complete ? '' : ' (partial history)'}.`);
  }
  for (const observer of observations.observers) {
    if (observer.state !== 'following') parts.push(`${observer.name} ${observer.state}${observer.detail ? `: ${observer.detail}` : ''}.`);
  }
  return parts.join(' ');
}
