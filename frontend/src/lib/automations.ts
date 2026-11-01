import type {
  AutomationAccess,
  AutomationAgent,
  AutomationOutcome,
  AutomationRun,
  AutomationSchedule,
  AutomationSpec,
  AutomationView
} from '../api/sessionsd/automations';

/** "Every 45 min", "Every 2 h", or "Daily at 09:00". */
export function scheduleText(schedule: AutomationSchedule): string {
  if (schedule.kind === 'daily') return `Daily at ${schedule.at}`;
  const minutes = schedule.everyMinutes;
  return minutes % 60 === 0 ? `Every ${minutes / 60} h` : `Every ${minutes} min`;
}

/** A clock time today, or a short weekday and time otherwise. */
export function automationClock(at: number, now = Date.now()): string {
  const date = new Date(at);
  const time = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false });
  return new Date(now).toDateString() === date.toDateString()
    ? time
    : `${date.toLocaleDateString([], { weekday: 'short' })} ${time}`;
}

export type AutomationTone = 'running' | 'attention' | 'failed' | 'paused' | 'idle';

/** The status dot: what the latest run says, or that the automation is paused. */
export function automationTone(view: AutomationView): AutomationTone {
  const outcome = view.lastRun?.outcome;
  if (outcome === 'starting' || outcome === 'running') return 'running';
  if (outcome === 'needs-input') return 'attention';
  if (!view.enabled) return 'paused';
  if (outcome === 'failed' || outcome === 'unknown') return 'failed';
  return 'idle';
}

/** The row's second line, such as "Every 2 h · next 14:05". */
export function automationSubline(view: AutomationView, now = Date.now()): string {
  const outcome = view.lastRun?.outcome;
  let state = view.enabled && view.nextRunAtMs ? `next ${automationClock(view.nextRunAtMs, now)}` : 'paused';
  if (outcome === 'starting' || outcome === 'running') state = 'running now';
  if (outcome === 'needs-input') state = 'needs you';
  return `${scheduleText(view.schedule)} · ${state}`;
}

const OUTCOME_LABELS: Record<AutomationOutcome, string> = {
  starting: 'Starting', running: 'Running', 'needs-input': 'Needs you', succeeded: 'Done',
  failed: 'Failed', skipped: 'Skipped', unknown: 'Unknown'
};

export function outcomeLabel(outcome: AutomationOutcome): string {
  return OUTCOME_LABELS[outcome] ?? outcome;
}

export function runIsSettled(run: AutomationRun): boolean {
  return run.outcome !== 'starting' && run.outcome !== 'running' && run.outcome !== 'needs-input';
}

/** The editor's form state. Numbers stay strings until saved. */
export interface AutomationDraft {
  name: string;
  prompt: string;
  cwd: string;
  agent: AutomationAgent;
  model: string;
  access: AutomationAccess;
  scheduleKind: AutomationSchedule['kind'];
  every: string;
  unit: 'minutes' | 'hours';
  at: string;
}

export function automationDraft(spec?: AutomationSpec, cwd = ''): AutomationDraft {
  const schedule = spec?.schedule ?? { kind: 'interval', everyMinutes: 60 };
  const minutes = schedule.kind === 'interval' ? schedule.everyMinutes : 60;
  const hours = minutes % 60 === 0;
  return {
    name: spec?.name ?? '', prompt: spec?.prompt ?? '', cwd: spec?.cwd ?? cwd,
    agent: spec?.agent ?? 'claude', model: spec?.model ?? '', access: spec?.access ?? 'full',
    scheduleKind: schedule.kind, every: String(hours ? minutes / 60 : minutes), unit: hours ? 'hours' : 'minutes',
    at: schedule.kind === 'daily' ? schedule.at : '09:00'
  };
}

/** The spec to save, or the first thing the person still needs to fix. */
export function draftSpec(draft: AutomationDraft, cwd: string): AutomationSpec | string {
  if (!draft.name.trim()) return 'Give the automation a name.';
  if (!draft.prompt.trim()) return 'Write the prompt each run starts with.';
  if (!cwd.trim()) return 'Choose the project folder it runs in.';
  let schedule: AutomationSchedule;
  if (draft.scheduleKind === 'daily') {
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(draft.at)) return 'Choose a time of day, such as 09:00.';
    schedule = { kind: 'daily', at: draft.at };
  } else {
    const count = Number(draft.every);
    const minutes = draft.unit === 'hours' ? count * 60 : count;
    if (!Number.isInteger(minutes) || minutes < 5 || minutes > 10080) return 'Run it every 5 minutes to 7 days, in whole minutes.';
    schedule = { kind: 'interval', everyMinutes: minutes };
  }
  return {
    name: draft.name.trim(), prompt: draft.prompt.trim(), cwd: cwd.trim(), agent: draft.agent,
    model: draft.model.trim() || undefined, access: draft.access, schedule
  };
}
