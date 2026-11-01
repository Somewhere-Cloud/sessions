import { lazy, Suspense, type ComponentProps } from 'react';

const AutomationsSection = lazy(() => import('./AutomationsSection'));

/** Automations load on their own, after the navigator, so they cost the entry nothing. */
export function LazyAutomationsSection(props: ComponentProps<typeof AutomationsSection>): JSX.Element {
  return <Suspense fallback={null}><AutomationsSection {...props} /></Suspense>;
}
