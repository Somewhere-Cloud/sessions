import { Button } from './ui/Button';
import { PageHeader, Stack } from './ui/Layout';

interface Props {
  onNew: () => void;
  hasSessions?: boolean;
}

export function EmptyState({ onNew, hasSessions = false }: Props): JSX.Element {
  return <div className="empty-state"><Stack className="empty-copy">
    <PageHeader eyebrow="Sessions" title={hasSessions ? 'Pick up where you left off' : 'Room for your next idea'}
      description={hasSessions ? 'Choose a session from your projects, or start something new.' : 'Work with Claude, Codex, or a shell. Your sessions keep running when you close the app.'} />
    <div><Button variant="primary" onClick={onNew}>＋ New Session</Button></div>
    <div className="empty-capabilities"><span>Local by default</span><span>Ready when you return</span></div>
  </Stack></div>;
}
