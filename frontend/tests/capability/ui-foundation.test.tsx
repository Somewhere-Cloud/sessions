import { createRef, useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Button, IconButton } from '../../src/components/ui/Button';
import { Field, Input, Select, Textarea } from '../../src/components/ui/Field';
import { EmptyState, Section, Status } from '../../src/components/ui/Layout';
import { TagEditor } from '../../src/components/TagEditor';

describe('shared control contracts', () => {
  it('defaults to a non-submit button, forwards refs and blocks activation while loading or disabled', async () => {
    const user = userEvent.setup(); const activate = vi.fn(); const submit = vi.fn(); const ref = createRef<HTMLButtonElement>();
    const { rerender } = render(<form onSubmit={submit}><Button ref={ref} onClick={activate}>Save</Button></form>);
    expect(ref.current).toBe(screen.getByRole('button', { name: 'Save' }));
    await user.click(ref.current!); await user.keyboard('{Enter} ');
    expect(activate).toHaveBeenCalledTimes(3); expect(submit).not.toHaveBeenCalled();
    rerender(<Button loading onClick={activate}>Saving…</Button>);
    expect(screen.getByRole('button')).toBeDisabled(); expect(screen.getByRole('button')).toHaveAttribute('aria-busy', 'true');
    await user.click(screen.getByRole('button')); expect(activate).toHaveBeenCalledTimes(3);
    rerender(<IconButton aria-label="Close review" disabled onClick={activate}><svg /></IconButton>);
    expect(screen.getByRole('button', { name: 'Close review' })).toBeDisabled();
  });

  it('associates labels, persistent help and changing errors without losing external descriptions', async () => {
    const user = userEvent.setup(); const ref = createRef<HTMLInputElement>();
    function Form(): JSX.Element {
      const [error, setError] = useState<string | null>(null);
      return <><div id="external-help">Used locally.</div><Field label="Project name" hint="Choose a short name." error={error}>
        <Input ref={ref} aria-describedby="external-help" /></Field>
        <Button onClick={() => setError('Add a name.')}>Validate</Button>
        <Field label="Mode"><Select defaultValue="local"><option value="local">Local</option></Select></Field>
        <Field label="Notes"><Textarea /></Field></>;
    }
    render(<Form />); const input = screen.getByLabelText('Project name');
    expect(input).toBe(ref.current); expect(input).toHaveAccessibleDescription('Choose a short name. Used locally.');
    await user.click(screen.getByText('Project name')); expect(input).toHaveFocus();
    await user.click(screen.getByRole('button', { name: 'Validate' }));
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Choose a short name. Add a name. Used locally.');
    expect(screen.getByRole('alert')).toHaveTextContent('Add a name.');
    expect(screen.getByLabelText('Mode')).toHaveValue('local'); expect(screen.getByLabelText('Notes').tagName).toBe('TEXTAREA');
  });

  it('associates tag validation, preserves removal and disables every mutation', async () => {
    const user = userEvent.setup(); const change = vi.fn();
    const { rerender } = render(<TagEditor value={{ project: 'Sessions' }} onChange={change} />);
    await user.type(screen.getByLabelText('Tag key'), '!'); await user.type(screen.getByLabelText('Tag value'), 'work');
    await user.click(screen.getByRole('button', { name: 'Add tag' }));
    expect(screen.getByLabelText('Tag key')).toHaveAccessibleDescription('Keys start with a letter or number and may use . _ or -');
    expect(change).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Remove project tag' })); expect(change).toHaveBeenCalledWith({});
    rerender(<TagEditor value={{ project: 'Sessions' }} onChange={change} disabled />);
    expect(screen.getAllByRole('textbox').every((control) => (control as HTMLInputElement).disabled)).toBe(true);
    expect(screen.getAllByRole('button').every((control) => (control as HTMLButtonElement).disabled)).toBe(true);
  });

  it('labels sections and gives async messages explicit live-region priority', () => {
    render(<Section title="Projects"><Status live="off">Unavailable on this computer</Status>
      <EmptyState title="No projects yet" action={<Button>Add project</Button>}>Choose a folder to begin.</EmptyState>
      <Status>Loading projects…</Status><Status tone="danger" live="assertive">Could not load projects. Retry.</Status></Section>);
    expect(screen.getByRole('region', { name: 'Projects' })).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Loading'); expect(screen.getByRole('alert')).toHaveTextContent('Retry');
    expect(screen.getByText('Unavailable on this computer')).not.toHaveAttribute('role');
  });
});
