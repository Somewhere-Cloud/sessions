import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { Button, IconButton } from '../src/components/ui/Button';
import { Field, Input, Select, Textarea } from '../src/components/ui/Field';
import { Actions, EmptyState, PageHeader, Section, Stack, Status, Surface } from '../src/components/ui/Layout';
import { ForkConfirmationDialog } from '../src/components/ForkConfirmationDialog';
import { RestartConversationDialog } from '../src/components/RestartConversationDialog';
import { TagEditor } from '../src/components/TagEditor';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

const params = new URLSearchParams(location.search);
const session = makeSession({ id: 'foundation-source', name: 'Sessions design review', tool: 'claude-code', cmd: 'claude', kind: 'claude-structured', permissions: 'constrained', cwd: '/Projects/Sessions' });
const machines: FakeMachine[] = [{ id: 'local', name: 'Fixture Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [session],
  providers: [{ id: 'claude', name: 'Claude Code', installed: true, updateAvailable: false,
    models: [{ id: 'fixture-model', displayName: 'Review model', hidden: false, isDefault: true, defaultReasoningEffort: 'medium', supportedReasoningEfforts: [] }] }] }];
const daemon = installFakeDaemon(machines);
useFakeMachines(machines);
const fakeFetch = window.fetch;
window.fetch = async (input, init) => {
  if (String(input).endsWith('/api/recovery/restart/preview')) {
    return new Response(JSON.stringify({ sourceSessionId: session.id, state: 'running', restartable: true, action: 'restart', accountChanged: false }), { headers: { 'content-type': 'application/json' } });
  }
  return fakeFetch(input, init);
};
Object.assign(window, { foundationDaemon: daemon });
// There is deliberately no product route to this test-only gallery.
function Gallery(): JSX.Element {
  const [tags, setTags] = useState({ project: 'Sessions' });
  const [theme, setTheme] = useState(params.get('theme') ?? 'dark');
  const [open, setOpen] = useState(params.get('screen') ?? 'gallery');
  return <main className={`operations-shell text-size-${params.get('size') ?? 's'}`} data-theme={theme}
    style={{ display: 'block', overflow: 'auto', padding: 24 }}>
    <div style={{ maxWidth: 860, margin: '0 auto' }}><Stack>
      <PageHeader title="Your workspace" eyebrow="Sessions" description="A calm place to keep work moving."
        actions={<Button onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>Switch theme</Button>} />
      <Surface><Section title="Project details" description="Names and tags stay yours.">
        <Field label="Project name" hint="Shown in your navigator."><Input defaultValue="Sessions" /></Field>
        <Field label="Computer"><Select defaultValue="local"><option value="local">This Mac</option><option value="mini">Mac mini</option></Select></Field>
        <Field label="Briefing" hint="Only shared when you start the agent."><Textarea rows={3} defaultValue="Review the interface and keep the original conversation running." /></Field>
        <TagEditor value={tags} onChange={(value) => setTags(value as typeof tags)} />
        <Actions><Button variant="ghost">Cancel</Button><Button variant="primary">Save changes</Button></Actions>
      </Section></Surface>
      <details className="fleet-refresh-notice"><summary>Some computers could not refresh. Saved connections are still available.</summary></details>
      <Section title="Control states"><Actions><IconButton aria-label="More actions">···</IconButton><Button size="compact">Compact</Button>
        <Button disabled>Unavailable</Button><Button loading>Saving…</Button><Button variant="danger">Remove project</Button></Actions>
        <Field label="Folder" error="Choose a folder on this computer."><Input defaultValue="/missing-folder" /></Field>
        <Status tone="success" live="off">Changes saved on this computer.</Status><Status>Refreshing saved projects…</Status><Status tone="warning" live="off">This computer is offline. Saved work is still available.</Status>
      </Section>
      <EmptyState title="No archived conversations" action={<Button>Browse conversations</Button>}>Conversations you archive will appear here, ready to restore.</EmptyState>
      <Actions><Button onClick={() => setOpen('fork')}>Review Add agent</Button><Button onClick={() => setOpen('restart')}>Review Restart</Button></Actions>
    </Stack></div>
    {open === 'fork' ? <ForkConfirmationDialog session={session} destinationProvider="claude" onClose={() => setOpen('gallery')} onStarted={() => {}} /> : null}
    {open === 'restart' ? <RestartConversationDialog session={session} onClose={() => setOpen('gallery')} onOpen={() => {}} /> : null}
  </main>;
}
createRoot(document.getElementById('root')!).render(<Gallery />);
