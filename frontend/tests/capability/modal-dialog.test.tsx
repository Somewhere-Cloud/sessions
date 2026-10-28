import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ModalDialog } from '../../src/components/ui/ModalDialog';
import { ModelPicker } from '../../src/components/ModelPicker';

function Review(): JSX.Element {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [child, setChild] = useState(false);
  return <>
    <button onClick={() => setOpen(true)}>Review</button>
    <button>Outside</button>
    {open ? <ModalDialog className="dialog" labelledBy="review-title" onClose={() => setOpen(false)} dismissible={!busy}>
      <h2 id="review-title">Review changes</h2>
      <button onClick={() => setBusy(!busy)}>Toggle busy</button>
      <button hidden>Hidden</button><div style={{ display: 'none' }}><button>Also hidden</button></div>
      <fieldset disabled><button>Disabled</button></fieldset>
      <ModelPicker provider="codex" value="" options={[{ id: 'astra', label: 'Astra' }]} onChange={() => {}} />
      <button onClick={() => setChild(true)}>Child</button>
      <button onClick={() => setOpen(false)}>Cancel</button>
      {child ? <ModalDialog className="dialog" labelledBy="child-title" onClose={() => setChild(false)}>
        <h2 id="child-title">Child review</h2><button>Child action</button>
      </ModalDialog> : null}
    </ModalDialog> : null}
  </>;
}

describe('modal keyboard and focus contract', () => {
  it('focuses the review, wraps Tab both ways, skips hidden/disabled controls and returns to the opener', async () => {
    const user = userEvent.setup(); render(<Review />);
    const opener = screen.getByRole('button', { name: 'Review' });
    await user.click(opener);
    expect(screen.getByRole('dialog', { name: 'Review changes' })).toHaveFocus();
    await user.tab(); expect(screen.getByRole('button', { name: 'Toggle busy' })).toHaveFocus();
    await user.tab(); expect(screen.getByRole('button', { name: /Default/ })).toHaveFocus();
    await user.tab(); expect(screen.getByRole('button', { name: 'Child' })).toHaveFocus();
    await user.tab(); expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
    await user.tab(); expect(screen.getByRole('button', { name: 'Toggle busy' })).toHaveFocus();
    await user.tab({ shift: true }); expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
    screen.getByRole('button', { name: 'Outside' }).focus();
    expect(screen.getByRole('dialog')).toHaveFocus();
    await user.keyboard('{Escape}'); expect(screen.queryByRole('dialog')).toBeNull(); expect(opener).toHaveFocus();
  });

  it('blocks Escape and backdrop dismissal while busy without leaking Escape to global shortcuts', async () => {
    const user = userEvent.setup(); render(<Review />);
    await user.click(screen.getByRole('button', { name: 'Review' }));
    const dialog = screen.getByRole('dialog'); const globalKey = vi.fn();
    window.addEventListener('keydown', globalKey);
    try {
      await user.click(screen.getByRole('button', { name: 'Toggle busy' }));
      globalKey.mockClear(); await user.keyboard('{Escape}');
      expect(globalKey).not.toHaveBeenCalled();
      fireEvent.mouseDown(dialog.parentElement!); expect(dialog).toBeInTheDocument();
      await user.click(screen.getByRole('button', { name: 'Toggle busy' }));
      fireEvent.mouseDown(dialog); expect(dialog).toBeInTheDocument();
      fireEvent.mouseDown(dialog.parentElement!); expect(screen.queryByRole('dialog')).toBeNull();
    } finally { window.removeEventListener('keydown', globalKey); }
  });

  it('keeps a portaled model picker usable and dismisses only that layer on Escape', async () => {
    const user = userEvent.setup(); render(<Review />);
    await user.click(screen.getByRole('button', { name: 'Review' }));
    const trigger = screen.getByRole('button', { name: /Default/ }); await user.click(trigger);
    const input = await screen.findByRole('combobox', { name: 'Search Codex models' });
    await vi.waitFor(() => expect(input).toHaveFocus());
    expect(input.closest('section')?.parentElement).toBe(document.body);
    await user.type(input, 'Ast'); expect(input).toHaveValue('Ast');
    await user.keyboard('{Escape}'); expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.getByRole('dialog')).toBeInTheDocument(); expect(trigger).toHaveFocus();
    await user.click(trigger); await screen.findByRole('combobox', { name: 'Search Codex models' });
    await user.click(screen.getByRole('option', { name: /Astra/ })); expect(trigger).toHaveFocus();
  });

  it('dismisses only the foremost modal, restoring focus through the stack', async () => {
    const user = userEvent.setup(); render(<Review />);
    await user.click(screen.getByRole('button', { name: 'Review' }));
    const childOpener = screen.getByRole('button', { name: 'Child' }); await user.click(childOpener);
    expect(screen.getByRole('dialog', { name: 'Child review' })).toHaveFocus();
    await user.keyboard('{Escape}'); expect(screen.queryByRole('dialog', { name: 'Child review' })).toBeNull();
    expect(childOpener).toHaveFocus(); expect(screen.getByRole('dialog', { name: 'Review changes' })).toBeInTheDocument();
    await user.keyboard('{Escape}'); expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('skips collapsed disclosure fields and includes them after expanding the disclosure', async () => {
    const user = userEvent.setup();
    render(<ModalDialog className="dialog" labelledBy="disclosure-title" onClose={() => {}}>
      <h2 id="disclosure-title">Account review</h2>
      <button>First</button>
      <details><summary>Options</summary><input aria-label="Advanced option" /></details>
    </ModalDialog>);
    const first = screen.getByRole('button', { name: 'First' });
    const summary = screen.getByText('Options', { selector: 'summary' });
    await user.tab({ shift: true }); expect(summary).toHaveFocus();
    await user.tab(); expect(first).toHaveFocus();
    await user.tab(); await user.click(summary);
    await user.tab(); expect(screen.getByRole('textbox', { name: 'Advanced option' })).toHaveFocus();
    await user.tab(); expect(first).toHaveFocus();
  });

  it('keeps focus when there are no enabled controls', async () => {
    const user = userEvent.setup();
    render(<ModalDialog className="dialog" labelledBy="empty-title" onClose={() => {}}>
      <h2 id="empty-title">Please wait</h2><button disabled>Working</button>
    </ModalDialog>);
    const dialog = screen.getByRole('dialog');
    await user.tab(); expect(dialog).toHaveFocus();
    await user.tab({ shift: true }); expect(dialog).toHaveFocus();
  });
});
