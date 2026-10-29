import { Button } from './ui/Button';
import { Field, Input } from './ui/Field';
import { Section, Surface } from './ui/Layout';
import { useEffect, useState } from 'react';
import { fetchRelayState, setRelayURL } from '../api/sessionsd';

export function RelayConnectionCard(): JSX.Element {
  const [url, setURL] = useState('');

  useEffect(() => {
    void fetchRelayState().then((state) => setURL(state.url)).catch(() => {});
  }, []);

  const save = (): void => {
    void setRelayURL(url.trim()).then((saved) => setURL(saved.url))
      .catch((reason) => window.alert(String(reason)));
  };

  return (
    <Surface><Section title="Relay fallback">
      <Field label="URL"><Input type="url" placeholder="https://relay" value={url} onChange={(event) => setURL(event.currentTarget.value)} /></Field>
      <Button onClick={save}>Save</Button>
    </Section></Surface>
  );
}
