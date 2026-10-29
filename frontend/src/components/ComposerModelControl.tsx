import { createPortal } from 'react-dom';
import { Button, IconButton } from './ui/Button';
import { Field, Select } from './ui/Field';
import { ModalDialog } from './ui/ModalDialog';
import { useEffect, useId, useState } from 'react';
import { listSessionModelOptions, type SessionModelOption } from '../api/sessionsd';
import type { SessionTool } from '../types';
import { CLAUDE_MODEL_OPTIONS, ModelPicker, type ModelPickerOption } from './ModelPicker';

interface Props {
  sessionId: string;
  provider: SessionTool;
  model?: string;
  effort?: string;
  supported: boolean;
  working: boolean;
  onChange: (model: string, effort: string) => Promise<void>;
}

function compactModel(model?: string): string {
  if (!model) return 'Choose model';
  const label = CLAUDE_MODEL_OPTIONS.find((option) => option.id === model)?.label;
  if (label) return label;
  return model.length > 24 ? `${model.slice(0, 21)}…` : model;
}

function pickerOptions(provider: SessionTool, options: SessionModelOption[]): ModelPickerOption[] {
  if (provider === 'claude-code') return CLAUDE_MODEL_OPTIONS;
  return options.map((option) => ({
    id: option.id,
    label: option.displayName || option.id,
    description: option.isDefault ? `Default · ${option.defaultReasoningEffort || 'automatic effort'}` : option.id,
    isDefault: option.isDefault
  }));
}

export function ComposerModelControl({
  sessionId,
  provider,
  model,
  effort,
  supported,
  working,
  onChange
}: Props): JSX.Element {
  const [open, setOpen] = useState(false);
  const [draftModel, setDraftModel] = useState(model ?? '');
  const [draftEffort, setDraftEffort] = useState(effort ?? '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [options, setOptions] = useState<SessionModelOption[]>([]);
  const [optionsLoading, setOptionsLoading] = useState(false);
  const [optionsError, setOptionsError] = useState<string | null>(null);
  const titleId = useId();

  useEffect(() => {
    if (open) return;
    setDraftModel(model ?? '');
    setDraftEffort(effort ?? '');
  }, [effort, model, open]);

  useEffect(() => {
    if (!open || !supported || provider !== 'codex') return;
    let live = true;
    setOptionsLoading(true);
    setOptionsError(null);
    void listSessionModelOptions(sessionId)
      .then((models) => {
        if (live) setOptions(models.filter((option) => !option.hidden));
      })
      .catch((caught) => {
        if (live) setOptionsError((caught as Error).message);
      })
      .finally(() => {
        if (live) setOptionsLoading(false);
      });
    return () => { live = false; };
  }, [open, provider, sessionId, supported]);

  const save = async (): Promise<void> => {
    if (!supported || working || !draftModel.trim()) return;
    setSaving(true);
    setError(null);
    try {
      await onChange(draftModel.trim(), draftEffort);
      setOpen(false);
    } catch (caught) {
      setError((caught as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const providerName = provider === 'codex' ? 'Codex' : 'Claude';
  const modelOptions = pickerOptions(provider, options);
  return (
    <div className="composer-model-control">
      <Button
        type="button"
        variant="ghost" size="compact" className="composer-model-trigger"
        onClick={() => { setError(null); setOpen((current) => !current); }}
        aria-expanded={open}
        aria-haspopup="dialog"
        title={supported
          ? 'Choose the model and effort for the next message'
          : `Model changes are unavailable for this ${providerName} session`}
      >
        <span>{compactModel(model)}</span>
        {effort ? <span>{effort}</span> : null}
        <span aria-hidden>⌄</span>
      </Button>
      {open ? createPortal(
        <ModalDialog className="dialog composer-model-dialog" labelledBy={titleId} onClose={() => setOpen(false)} dismissible={!saving}>
          <header>
            <h2 id={titleId}>{providerName} · next message</h2>
            <IconButton variant="ghost" aria-label="Close model selector" disabled={saving} onClick={() => setOpen(false)}>×</IconButton>
          </header>
          {!supported ? (
            <p>Model changes are unavailable in this conversation. Choose a model when starting a new {providerName} session.</p>
          ) : (
            <>
              <div className="composer-model-field">
                <span>Model</span>
                <ModelPicker
                  provider={provider === 'codex' ? 'codex' : 'claude'}
                  value={draftModel}
                  options={modelOptions}
                  onChange={setDraftModel}
                  loading={optionsLoading}
                  error={optionsError}
                  includeDefault={false}
                  defaultLabel="Choose model"
                  allowCustom
                  compact
                />
              </div>
              <Field label="Effort">
                <Select value={draftEffort} onChange={(event) => setDraftEffort(event.currentTarget.value)}>
                  <option value="">Automatic</option>
                  <option value="low">Low</option>
                  <option value="medium">Medium</option>
                  <option value="high">High</option>
                  <option value="xhigh">Extra high</option>
                  {provider === 'claude-code' ? <option value="max">Max</option> : null}
                </Select>
              </Field>
              <p>
                {working
                  ? `Wait for ${providerName} to finish this turn.`
                  : optionsLoading
                    ? 'Loading available Codex models…'
                    : optionsError
                      ? 'Could not load the live catalog. You can still enter an exact model ID.'
                      : 'Applies to the next message. Existing history is unchanged.'}
              </p>
              {error ? <p className="composer-model-error" role="alert">{error}</p> : null}
              <Button
                type="button"
                variant="primary" className="composer-model-save"
                disabled={saving || working || !draftModel.trim()}
                onClick={() => void save()}
              >
                {saving ? 'Saving…' : 'Use for next message'}
              </Button>
            </>
          )}
        </ModalDialog>, document.body
      ) : null}
    </div>
  );
}
