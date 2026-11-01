import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import AutomationsSection from '../../src/components/AutomationsSection';
import type { AutomationsListing, AutomationView } from '../../src/api/sessionsd/automations';
import { automationSubline, draftSpec, automationDraft, scheduleText } from '../../src/lib/automations';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from './fake-daemon';

const machines: FakeMachine[] = [{ id: 'local', name: 'Studio', host: 'localhost', port: 8787, isDefault: true, sessions: [] }];
const emptySummary = { total: 0, skipped: 0, running: 0, needsInput: 0, recentFailed: 0 };

function triage(overrides: Partial<AutomationView> = {}): AutomationView {
  return {
    id: '1a2b3c4d', name: 'Nightly triage', prompt: 'Triage new issues', cwd: '/work/app', agent: 'claude', access: 'full',
    schedule: { kind: 'interval', everyMinutes: 120 }, enabled: true, createdAtMs: 1, updatedAtMs: 1,
    nextRunAtMs: new Date(2026, 10, 1, 14, 5).getTime(), runs: emptySummary, ...overrides
  };
}

function stubDaemon(listing: () => AutomationsListing | null): Array<{ method: string; url: string; body: unknown }> {
  installFakeDaemon(machines); useFakeMachines(machines, 'local');
  const calls: Array<{ method: string; url: string; body: unknown }> = [];
  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
    const current = listing();
    if (!current) return new Response('{}', { status: 404 });
    if (url.endsWith('/api/automations') && method === 'GET') return Response.json(current);
    if (url.endsWith('/api/automations') && method === 'POST') return Response.json(triage(), { status: 201 });
    if (url.includes('/api/automations/runs')) {
      return Response.json({ runs: [
        { id: 'r2', automationId: '1a2b3c4d', automationName: 'Nightly triage', trigger: 'schedule', outcome: 'failed', detail: 'account needs login', sessionId: 'session-2', startedAtMs: 2, updatedAtMs: 2 },
        { id: 'r1', automationId: '1a2b3c4d', automationName: 'Nightly triage', trigger: 'schedule', outcome: 'skipped', detail: 'previous run open', startedAtMs: 1, updatedAtMs: 1 }
      ], summary: current.runs });
    }
    if (url.endsWith('/run')) return Response.json({ id: 'r3', outcome: 'starting' }, { status: 202 });
    if (url.endsWith('/enabled')) return Response.json(triage({ enabled: false }));
    throw new Error(`unexpected ${method} ${url}`);
  });
  return calls;
}

it('stays a single quiet link until the first automation exists, then creates one', async () => {
  const calls = stubDaemon(() => ({ automations: [], runs: emptySummary }));
  render(<AutomationsSection defaultCwd="/work/app" onOpenSession={() => undefined} />);
  const link = await screen.findByRole('button', { name: /New automation/ });
  expect(screen.queryByText('Previous agents')).not.toBeInTheDocument();
  fireEvent.click(link);
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'Nightly triage' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create automation' }));
  expect(await within(dialog).findByText('Write the prompt each run starts with.')).toBeInTheDocument();
  fireEvent.change(within(dialog).getByLabelText(/Prompt/), { target: { value: 'Triage new issues' } });
  fireEvent.change(within(dialog).getByLabelText('Interval'), { target: { value: '2' } });
  fireEvent.change(within(dialog).getByLabelText('Unit'), { target: { value: 'hours' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Create automation' }));
  await waitFor(() => expect(calls.some((call) => call.method === 'POST')).toBe(true));
  expect(calls.find((call) => call.method === 'POST')?.body).toMatchObject({
    name: 'Nightly triage', prompt: 'Triage new issues', cwd: '/work/app', agent: 'claude', access: 'full',
    schedule: { kind: 'interval', everyMinutes: 120 }, enabled: true
  });
});

it('shows each automation with its schedule, run, toggle and the previous agents row', async () => {
  const listing: AutomationsListing = {
    automations: [triage({ lastRun: { id: 'r2', automationId: '1a2b3c4d', automationName: 'Nightly triage', trigger: 'schedule', outcome: 'failed', startedAtMs: 2, updatedAtMs: 2 } })],
    runs: { total: 121, skipped: 4, running: 0, needsInput: 0, recentFailed: 3 }
  };
  const calls = stubDaemon(() => listing);
  const opened: string[] = [];
  render(<AutomationsSection onOpenSession={(id) => opened.push(id)} />);
  const section = await screen.findByRole('region', { name: 'Automations' });
  expect(within(section).getByText('Nightly triage')).toBeInTheDocument();
  expect(within(section).getByText(/^Every 2 h · next/)).toBeInTheDocument();
  expect(within(section).getByText('3 failed')).toBeInTheDocument();
  expect(within(section).getByText('121')).toBeInTheDocument();
  fireEvent.click(within(section).getByRole('button', { name: 'Run Nightly triage now' }));
  await waitFor(() => expect(calls.some((call) => call.method === 'POST' && call.url.endsWith('/api/automations/1a2b3c4d/run'))).toBe(true));
  const toggle = within(section).getByRole('switch', { name: 'Nightly triage on schedule' });
  await waitFor(() => expect(toggle).toBeEnabled());
  fireEvent.click(toggle);
  await waitFor(() => expect(calls.find((call) => call.url.endsWith('/enabled'))?.body).toEqual({ enabled: false }));
  fireEvent.click(within(section).getByRole('button', { name: /Previous agents: 121, 3 failed/ }));
  const history = await screen.findByRole('dialog');
  expect(await within(history).findByText(/account needs login/)).toBeInTheDocument();
  expect(within(history).getByText('Skipped')).toBeInTheDocument();
  fireEvent.click(within(history).getByRole('button', { name: 'Needs a look' }));
  expect(within(history).queryByText('Skipped')).not.toBeInTheDocument();
  fireEvent.click(within(history).getByRole('button', { name: 'Open session' }));
  expect(opened).toEqual(['session-2']);
});

it('renders nothing on a runtime without automations', async () => {
  const calls = stubDaemon(() => null);
  const { container } = render(<AutomationsSection onOpenSession={() => undefined} />);
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(container).toBeEmptyDOMElement();
});

it('describes schedules and refuses drafts the daemon would refuse', () => {
  expect(scheduleText({ kind: 'interval', everyMinutes: 45 })).toBe('Every 45 min');
  expect(scheduleText({ kind: 'daily', at: '09:00' })).toBe('Daily at 09:00');
  expect(automationSubline(triage({ enabled: false }))).toBe('Every 2 h · paused');
  expect(automationSubline(triage({ lastRun: { id: 'r', automationId: 'a', automationName: 'n', trigger: 'manual', outcome: 'needs-input', startedAtMs: 1, updatedAtMs: 1 } }))).toBe('Every 2 h · needs you');
  const draft = { ...automationDraft(undefined, '/work'), name: 'n', prompt: 'p', every: '2', unit: 'minutes' as const };
  expect(draftSpec(draft, '/work')).toBe('Run it every 5 minutes to 7 days, in whole minutes.');
  expect(draftSpec({ ...draft, scheduleKind: 'daily', at: '9am' }, '/work')).toBe('Choose a time of day, such as 09:00.');
});
