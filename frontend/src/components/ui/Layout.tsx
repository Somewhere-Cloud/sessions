import { useId, type HTMLAttributes, type ReactNode } from 'react';

type BoxProps = HTMLAttributes<HTMLDivElement>;
export function Surface({ className = '', ...props }: BoxProps): JSX.Element {
  return <div {...props} className={`ui-surface ${className}`} />;
}
export function Stack({ className = '', ...props }: BoxProps): JSX.Element {
  return <div {...props} className={`ui-stack ${className}`} />;
}
export function Actions({ className = '', ...props }: HTMLAttributes<HTMLElement>): JSX.Element {
  return <footer {...props} className={`ui-actions ${className}`} />;
}
export interface HeadingProps {
  title: ReactNode;
  titleId?: string;
  description?: ReactNode;
  eyebrow?: ReactNode;
  actions?: ReactNode;
  level?: 1 | 2 | 3;
}
export function PageHeader({ title, titleId, description, eyebrow, actions, level = 1 }: HeadingProps): JSX.Element {
  const Heading = `h${level}` as 'h1' | 'h2' | 'h3';
  return <header className="ui-header">
    <div>{eyebrow ? <div className="ui-eyebrow">{eyebrow}</div> : null}
      <Heading id={titleId}>{title}</Heading>
      {description ? <div className="ui-description">{description}</div> : null}
    </div>{actions ? <div className="ui-actions">{actions}</div> : null}
  </header>;
}
export function Section({ children, ...heading }: HeadingProps & { children: ReactNode }): JSX.Element {
  const generated = useId();
  const id = heading.titleId ?? generated;
  return <section className="ui-stack" aria-labelledby={id}>
    <PageHeader {...heading} titleId={id} level={heading.level ?? 2} />{children}
  </section>;
}
export function Status({ children, tone = 'neutral', live = 'polite' }: {
  children: ReactNode; tone?: 'neutral' | 'success' | 'warning' | 'danger'; live?: 'off' | 'polite' | 'assertive';
}): JSX.Element {
  return <div className="ui-status" data-tone={tone} role={live === 'off' ? undefined : live === 'assertive' ? 'alert' : 'status'}>{children}</div>;
}
export function EmptyState({ title, children, action }: { title: string; children: ReactNode; action?: ReactNode }): JSX.Element {
  return <Surface><Stack><strong>{title}</strong><div className="ui-description">{children}</div>{action}</Stack></Surface>;
}
