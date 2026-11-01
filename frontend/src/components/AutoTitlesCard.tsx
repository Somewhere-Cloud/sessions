import { Surface, PageHeader } from './ui/Layout';
import { useEffect, useRef, useState } from 'react';
import { fetchAutoTitles, updateAutoTitles, type AutoTitleSettings } from '../api/sessionsd';

interface Props {
  clientOnly: boolean;
}

// The machine-wide automatic title switch, `sessions titles on|off`.
export function AutoTitlesCard({ clientOnly }: Props): JSX.Element {
  const [status, setStatus] = useState<AutoTitleSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const generation = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    const current = generation.current + 1;
    generation.current = current;
    void fetchAutoTitles(controller.signal)
      .then((value) => { if (generation.current === current) setStatus(value); })
      .catch(() => {
        if (!controller.signal.aborted && generation.current === current) {
          setMessage('Automatic titles require a current Sessions runtime.');
        }
      });
    return () => controller.abort();
  }, []);

  const save = async (enabled: boolean): Promise<void> => {
    setBusy(true);
    setMessage(null);
    try {
      setStatus(await updateAutoTitles(enabled));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : 'Could not save the automatic title setting.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Surface className="settings-card settings-status-card">
      <PageHeader level={2} title="Name sessions automatically" />
      <p>
        After the first finished turn of a Claude or Codex session that nobody named, Sessions asks that session’s own
        CLI and account for a short title. Claude uses Haiku. The request holds the first message and the reply, cut to
        about 1,500 characters each, and goes only to the provider that already has the conversation. It runs without
        tools or a saved session, gives up after 45 seconds, and never slows the session.
      </p>
      <label className="settings-toggle">
        <span>
          <strong>Generate a title once</strong>
          <small>Claude’s own title is used when there is one. Renaming a session always wins.</small>
        </span>
        <input
          type="checkbox"
          checked={status?.enabled ?? true}
          disabled={clientOnly || busy || status === null}
          onChange={(event) => void save(event.currentTarget.checked)}
        />
      </label>
      {message ? <div className="settings-message" role="status">{message}</div> : null}
    </Surface>
  );
}
