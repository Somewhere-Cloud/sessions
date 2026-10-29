import { createContext, forwardRef, useContext, useId, type InputHTMLAttributes, type SelectHTMLAttributes, type TextareaHTMLAttributes, type ReactNode } from 'react';

interface FieldAssociation { id: string; describedBy?: string; invalid: boolean }
const FieldContext = createContext<FieldAssociation | undefined>(undefined);
export interface FieldProps {
  id?: string;
  label: ReactNode;
  hint?: ReactNode;
  error?: string | null;
  children: ReactNode;
  className?: string;
}

/** One labelable control per Field. Explicit control IDs must match Field.id. */
export function Field({ id, label, hint, error, children, className = '' }: FieldProps): JSX.Element {
  const generated = useId();
  const controlId = id ?? generated;
  const describedBy = [hint ? `${controlId}-hint` : '', error ? `${controlId}-error` : ''].filter(Boolean).join(' ') || undefined;
  return <div className={`field ui-field ${className}`}>
    <label className="field-label" htmlFor={controlId}>{label}</label>
    <FieldContext.Provider value={{ id: controlId, describedBy, invalid: Boolean(error) }}>{children}</FieldContext.Provider>
    {hint ? <div className="field-help" id={`${controlId}-hint`}>{hint}</div> : null}
    {error ? <div className="ui-error" id={`${controlId}-error`} role="alert">{error}</div> : null}
  </div>;
}

function useAssociation(props: { id?: string; 'aria-describedby'?: string; 'aria-invalid'?: InputHTMLAttributes<HTMLInputElement>['aria-invalid'] }) {
  const field = useContext(FieldContext);
  return {
    id: props.id ?? field?.id,
    'aria-describedby': [field?.describedBy, props['aria-describedby']].filter(Boolean).join(' ') || undefined,
    'aria-invalid': props['aria-invalid'] ?? (field?.invalid || undefined)
  };
}

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className = '', ...props }, ref) {
  return <input {...props} {...useAssociation(props)} ref={ref} className={`field-input ui-control ${className}`} />;
});

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(function Select({ className = '', ...props }, ref) {
  return <select {...props} {...useAssociation(props)} ref={ref} className={`field-input ui-control ${className}`} />;
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea({ className = '', ...props }, ref) {
  return <textarea {...props} {...useAssociation(props)} ref={ref} className={`field-input ui-control ${className}`} />;
});
