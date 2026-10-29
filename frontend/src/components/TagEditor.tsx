import { useId, useMemo, useState } from 'react';
import { Button, IconButton } from './ui/Button';
import { Field, Input } from './ui/Field';
import { Surface } from './ui/Layout';

interface Props {
  value: Record<string, string>;
  onChange: (value: Record<string, string>) => void;
  disabled?: boolean;
}

const TAG_KEY = /^[a-z0-9][a-z0-9._-]*$/;

export function TagEditor({ value, onChange, disabled = false }: Props): JSX.Element {
  const errorId = useId();
  const [key, setKey] = useState('');
  const [tagValue, setTagValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const entries = useMemo(
    () => Object.entries(value).sort(([left], [right]) => left.localeCompare(right)),
    [value]
  );

  const add = (): void => {
    const normalizedKey = key.trim().toLowerCase();
    const normalizedValue = tagValue.trim();
    if (!TAG_KEY.test(normalizedKey) || normalizedKey.length > 64) {
      setError('Keys start with a letter or number and may use . _ or -');
      return;
    }
    if (!normalizedValue || normalizedValue.length > 256) {
      setError('Values must be between 1 and 256 characters');
      return;
    }
    if (!(normalizedKey in value) && entries.length >= 32) {
      setError('A session can have up to 32 tags');
      return;
    }
    onChange({ ...value, [normalizedKey]: normalizedValue });
    setKey('');
    setTagValue('');
    setError(null);
  };

  return (
    <Surface className="tag-editor">
      {entries.length > 0 ? (
        <div className="tag-chip-list" aria-label="Session tags">
          {entries.map(([tagKey, currentValue]) => (
            <span className="tag-chip" key={tagKey}>
              <span className="tag-chip-key">{tagKey}</span>
              <span className="tag-chip-value">{currentValue}</span>
              <IconButton
                size="compact"
                aria-label={`Remove ${tagKey} tag`}
                disabled={disabled}
                onClick={() => {
                  const next = { ...value };
                  delete next[tagKey];
                  onChange(next);
                }}
              >
                ×
              </IconButton>
            </span>
          ))}
        </div>
      ) : null}
      <div className="tag-editor-row">
        <Field label="Tag key"><Input
          aria-describedby={error ? errorId : undefined}
          aria-invalid={Boolean(error)}
          type="text"
          placeholder="product"
          value={key}
          maxLength={64}
          disabled={disabled}
          onChange={(event) => setKey(event.target.value)}
        /></Field>
        <Field label="Tag value"><Input
          aria-describedby={error ? errorId : undefined}
          aria-invalid={Boolean(error)}
          type="text"
          placeholder="Sessions"
          value={tagValue}
          maxLength={256}
          disabled={disabled}
          onChange={(event) => setTagValue(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              add();
            }
          }}
        /></Field>
        <Button variant="ghost"
          disabled={disabled || !key.trim() || !tagValue.trim()}
          onClick={add}
        >
          Add tag
        </Button>
      </div>
      {error ? <span id={errorId} className="ui-error" role="alert">{error}</span> : null}
      <span className="field-help">Use any dimensions you care about, such as product, client, project, or environment.</span>
    </Surface>
  );
}
