import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/styles/globals.css';
import { NewSessionDialog } from '../src/components/NewSessionDialog';
import { OnboardingDialog } from '../src/components/OnboardingDialog';
import { PaidStartPlan } from '../src/components/PaidStartPlan';
import { SettingsMenu } from '../src/components/SettingsMenu';
import { ProviderHandoff } from '../src/components/ProviderHandoff';
import { SessionActionsMenu } from '../src/components/SessionActionsMenu';
import { InlineAccountSignIn } from '../src/components/InlineAccountSignIn';
import { Button } from '../src/components/ui/Button';
import { usePaidStartPlan } from '../src/hooks/usePaidStartPlan';
import { installFakeDaemon, makeSession, useFakeMachines, type FakeMachine } from '../tests/capability/fake-daemon';

const params = new URLSearchParams(location.search);
const model = { id: 'fixture-model', displayName: 'Fixture model', hidden: false, isDefault: true, supportedReasoningEfforts: [{ reasoningEffort: 'high', description: 'High' }] };
const machine: FakeMachine = { id: 'local', name: 'Studio Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [],
  directories: [{ path: '/work/Sessions', label: 'Sessions', kind: 'project' }], codexModels: [model],
  providers: [{ id: 'claude', name: 'Claude Code', installed: true, updateAvailable: false, models: [model] }, { id: 'codex', name: 'Codex', installed: true, updateAvailable: false, models: [model] }]
};
const daemon = installFakeDaemon([machine]);
useFakeMachines([machine]);
const choices: unknown[] = [];
Object.assign(window, { polishDaemon: daemon, polishChoices: choices });
function Fixture(): JSX.Element {
  const [open, setOpen] = useState(false);
  const plan = usePaidStartPlan({ sourceProvider: 'claude', terminalAvailable: true });
  const screen = params.get('screen');
  return <div className={`operations-shell text-size-${params.get('scale') === 'l' ? 'l' : 'm'}`} data-theme={params.get('theme') === 'light' ? 'light' : 'dark'} style={{ height: '100dvh', overflow: 'auto' }}>
    <main style={{ padding: 16 }}>
      {screen === 'onboarding' ? <OnboardingDialog machine="Studio Mac" busy={false} error={null} onAllowLocalNetwork={async () => { choices.push('network'); }} onChoose={async (...choice) => { choices.push(choice); }} />
        : screen === 'paid' ? <PaidStartPlan plan={plan} title="Review a new paid session" intro="Review the model and permissions before starting." sourceNote="The original conversation stays on Studio Mac." />
        : screen === 'menus' ? <><div style={{ display: 'flex', justifyContent: 'flex-end' }}><SettingsMenu textSize="m" onTextSizeChange={() => {}} /></div><SessionActionsMenu><ProviderHandoff session={makeSession({ id: 'fixture-session', kind: 'claude-structured' })} serverId="local" onOpen={() => {}} /><Button>Session details</Button><Button>Copy session link</Button></SessionActionsMenu><InlineAccountSignIn tool="claude" serverId="local" onReload={() => {}} onConnected={() => {}} /></>
        : <><Button onClick={() => setOpen(true)}>New session</Button>{open ? <NewSessionDialog onClose={() => setOpen(false)} onStarted={() => {}} /> : null}</>}
    </main>
  </div>;
}
createRoot(document.getElementById('root')!).render(<Fixture />);
