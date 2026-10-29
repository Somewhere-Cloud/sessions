import { useId } from 'react';
import { Select } from './ui/Field';
import type { ReactNode } from 'react';
import type { SessionModelOption } from '../api/sessionsd';
import type { PaidStartPlanState, PaidStartProvider } from '../hooks/usePaidStartPlan';
import { ModelPicker, type ModelPickerOption } from './ModelPicker';

interface Props {
  plan: PaidStartPlanState;
  title: string;
  sizeLine?: string;
  intro: string;
  sourceNote: string;
  copyNote?: string;
  disabled?: boolean;
  allowAgentChoice?: boolean;
  children?: ReactNode;
  assurance?: string;
  compact?: boolean;
}

export function paidStartProviderName(provider: PaidStartProvider): string {
  return provider === 'claude' ? 'Claude' : 'Codex';
}

function modelOptions(models: SessionModelOption[]): ModelPickerOption[] {
  return models.map((model) => ({
    id: model.id,
    label: model.displayName || model.id,
    description: model.description || (model.isDefault ? 'Provider default' : model.id),
    isDefault: model.isDefault
  }));
}

function PlanChoice({ label, children }: { label: string; children: ReactNode }): JSX.Element {
  return <div className="paid-start-choice"><span>{label}</span>{children}</div>;
}

function AgentChoice({ plan, disabled }: { plan: PaidStartPlanState; disabled: boolean }): JSX.Element {
  const name = useId();
  return (
    <PlanChoice label="Agent">
      <fieldset className="paid-start-segments" aria-label="Agent">
        {(['claude', 'codex'] as const).map((provider) => (
          <label className={plan.destination === provider ? 'is-active' : ''} key={provider}>
            <input type="radio" name={name} checked={plan.destination === provider} disabled={disabled} onChange={() => plan.setDestination(provider)} />
            <span>{paidStartProviderName(provider)}</span>
          </label>
        ))}
      </fieldset>
    </PlanChoice>
  );
}

function RuntimeChoice({ plan, disabled }: { plan: PaidStartPlanState; disabled: boolean }): JSX.Element {
  const name = useId();
  return (
    <PlanChoice label="Runtime">
      <fieldset className="paid-start-segments" aria-label="Runtime">
        <label className={plan.runtime === 'rich' ? 'is-active' : ''}>
          <input type="radio" name={name} checked={plan.runtime === 'rich'} disabled={disabled} onChange={() => plan.setRuntime('rich')} /><span>Rich</span>
        </label>
        <label className={plan.runtime === 'terminal' ? 'is-active' : ''}>
          <input type="radio" name={name} checked={plan.runtime === 'terminal'} disabled={disabled || !plan.terminalAvailable} onChange={() => plan.setRuntime('terminal')} /><span>Terminal</span>
        </label>
      </fieldset>
      {!plan.terminalAvailable ? <small>This plan starts in Rich; Terminal is unavailable.</small> : null}
    </PlanChoice>
  );
}

function ConfigurationRow({ plan, disabled, allowAgentChoice, compact }: {
  plan: PaidStartPlanState;
  disabled: boolean;
  allowAgentChoice: boolean;
  compact: boolean;
}): JSX.Element {
  const selectedModel = plan.models.find((entry) => entry.id === plan.model);
  const efforts = selectedModel?.supportedReasoningEfforts ?? [];
  return (
    <div className="paid-start-plan-row" role="group" aria-label="Start plan">
      {allowAgentChoice ? <AgentChoice plan={plan} disabled={disabled} /> : (
        <PlanChoice label="Agent"><strong>{paidStartProviderName(plan.destination)}</strong></PlanChoice>
      )}
      <PlanChoice label="Model">
        <ModelPicker provider={plan.destination} value={plan.model} options={modelOptions(plan.models)} onChange={plan.setModel} loading={plan.modelsLoading} error={plan.modelError} disabled={disabled} includeDefault={false} />
      </PlanChoice>
      <PlanChoice label="Effort">
        {efforts.length > 0 ? (
          <Select aria-label="Effort" value={plan.effort} disabled={disabled} onChange={(event) => plan.setEffort(event.currentTarget.value)}>
            {!selectedModel?.defaultReasoningEffort ? <option value="">Provider default</option> : null}
            {efforts.map((option) => <option value={option.reasoningEffort} key={option.reasoningEffort}>{option.reasoningEffort}</option>)}
          </Select>
        ) : <strong>{plan.effort || 'Provider default'}</strong>}
      </PlanChoice>
      {!compact ? <RuntimeChoice plan={plan} disabled={disabled} /> : null}
      {!compact ? <PlanChoice label="Access policy"><strong>{plan.access}</strong></PlanChoice> : null}
    </div>
  );
}

export function PaidStartPlan({
  plan,
  title,
  sizeLine,
  intro,
  sourceNote,
  copyNote,
  disabled = false,
  allowAgentChoice = true,
  children,
  assurance = 'Nothing runs until you press Start',
  compact = false
}: Props): JSX.Element {
  return (
    <section className="continuation-plan paid-start-plan">
      <div className="continuation-summary">
        <strong>{title}</strong>
        <span>{intro}</span>
        {sizeLine ? <span>{sizeLine}</span> : null}
      </div>
      <ConfigurationRow plan={plan} disabled={disabled} allowAgentChoice={allowAgentChoice} compact={compact} />
      {compact ? <p className="paid-start-permission-note">Permissions: {plan.access}</p> : null}
      {children}
      <div className="paid-start-boundary">
        <span>{sourceNote}</span>
        {copyNote ? <span>{copyNote}</span> : null}
        {plan.destination === 'claude' ? <span>Remote Control follows the explicit choice for the destination machine in Settings.</span> : null}
      </div>
      <div className="continuation-assurances">
        <span>{assurance}</span>
      </div>
      {plan.modelError ? <div className="dialog-error" role="alert">{plan.modelError}</div> : null}
    </section>
  );
}
