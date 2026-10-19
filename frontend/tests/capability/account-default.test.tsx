import { expect, it } from 'vitest';
import { render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AccountsView } from '../../src/components/AccountsView';
import { NewSessionDialog } from '../../src/components/NewSessionDialog';
import { installFakeDaemon, useFakeMachines, type FakeMachine } from './fake-daemon';

it('makes an account the machine default and preselects it for new sessions', async () => {
  const machines: FakeMachine[] = [{ id: 'local', name: 'This Mac', host: 'localhost', port: 8787, isDefault: true, sessions: [], profiles: [
    { tool: 'claude', name: '', default_profile: true, preferred: true, identity: { email: 'old@example.test', checked_at: Date.now() } },
    { tool: 'claude', name: 'bean', identity: { email: 'bean@example.test', checked_at: Date.now() } }
  ] }];
  installFakeDaemon(machines);
  useFakeMachines(machines, 'local');
  const view = render(<AccountsView hostName="This Mac" serverId="local" />);
  const computers = await screen.findByRole('list', { name: 'Computers for bean@example.test' });
  await userEvent.click(within(computers).getByRole('button', { name: 'Make default' }));
  await waitFor(() => expect(within(computers).getByRole('button', { name: 'Default for new sessions' })).toBeDisabled());
  expect(machines[0]!.profiles![1]!.preferred).toBe(true);
  view.unmount();
  render(<NewSessionDialog onClose={() => {}} onStarted={() => {}} />);
  await waitFor(() => expect(screen.getByLabelText('Account')).toHaveValue('bean'));
  await userEvent.selectOptions(screen.getByLabelText('Account'), '');
  expect(screen.getByLabelText('Account')).toHaveValue('');
  expect(machines[0]!.sessions).toHaveLength(0);
});
