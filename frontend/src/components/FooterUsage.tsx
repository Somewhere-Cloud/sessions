import { lazy, Suspense, useEffect, useRef } from 'react';
import type { ComposerUsageScope } from '../lib/composerAccountUsage';
import { Button } from './ui/Button';
import '../styles/footer-usage.css';

const SelectedUsage = lazy(() => import('./FooterUsageAccount').then((module) => ({ default: module.SelectedUsage })));

export interface FooterUsageProps {
  usageScope?: ComposerUsageScope;
  onOpenUsage: () => void;
  onOpenDaily: () => void;
}

export function FooterUsage(props: FooterUsageProps): JSX.Element {
  return props.usageScope && props.usageScope.provider !== 'terminal'
    ? <Suspense fallback={<UsageDisclosure {...props} summary="Reading limits…" />}><SelectedUsage key={JSON.stringify(props.usageScope)} {...props} scope={props.usageScope} /></Suspense>
    : <UsageDisclosure {...props} summary="No selected account" />;
}

export function UsageDisclosure({ summary, children, onOpenUsage, onOpenDaily }: FooterUsageProps & {
  summary: string; children?: React.ReactNode;
}): JSX.Element {
  const details = useRef<HTMLDetailsElement>(null);
  const close = () => { if (details.current) details.current.open = false; };
  useEffect(() => {
    const outside = (event: PointerEvent) => {
      if (!details.current?.contains(event.target as Node)) close();
    };
    document.addEventListener('pointerdown', outside);
    return () => document.removeEventListener('pointerdown', outside);
  }, []);
  return <details ref={details} className="footer-usage" onKeyDown={(event) => {
    if (event.key === 'Escape' && details.current?.open) {
      event.preventDefault(); event.stopPropagation(); close();
      details.current.querySelector('summary')?.focus();
    }
  }} onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) close(); }}>
    <summary title={`Usage · ${summary}`} aria-label={`Usage · ${summary}`}>
      <svg aria-hidden viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7"><path d="M4 20V10M10 20V4M16 20v-7M22 20H2" /></svg>
      <span className="footer-usage-caption"><strong>Usage</strong><small className="footer-usage-full">{summary}</small><small className="footer-usage-short">{shortUsage(summary)}</small></span>
      <span className="footer-usage-rail-reading" aria-hidden>{shortUsage(summary)}</span>
    </summary>
    <section className="footer-usage-popover" aria-label="Account limits">
      <h2>Account limits</h2>
      {children ?? <p>Select an agent session to see its account limits.</p>}
      <div className="footer-usage-links">
        <Button variant="ghost" size="compact" onClick={() => { close(); onOpenUsage(); }}>Open usage</Button>
        <Button variant="ghost" size="compact" onClick={() => { close(); onOpenDaily(); }}>Daily activity</Button>
      </div>
    </section>
  </details>;
}

function shortUsage(summary: string): string {
  return summary.replace('Last known · ', 'Last · ').replace(/(\d+)-hour limit /, '$1h · ')
    .replace(/(\d+)-minute limit /, '$1m · ').replace('Weekly limit ', 'Week · ')
    .replace(' independent limits', ' limits').replace(' used', '').replace(' · +1 window', ' +1')
    .replace('Limits unavailable', 'Unavailable').replace('Limits unknown', 'Unknown')
    .replace('No selected account', 'No account').replace('Reading limits…', 'Reading…');
}
