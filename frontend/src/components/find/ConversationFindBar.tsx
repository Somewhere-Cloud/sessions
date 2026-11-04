import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type RefObject } from 'react';
import { findRanges, revealRange } from '../../lib/conversationFind';
import './conversationFind.css';

interface Props {
  // The scrolling transcript to search.
  scroller: RefObject<HTMLElement | null>;
  // Changes each time find is asked for again while open.
  request: number;
  onClose: () => void;
}

type HighlightRegistry = { set: (name: string, value: unknown) => void; delete: (name: string) => void };
type HighlightConstructor = new (...ranges: Range[]) => unknown;

function highlightApi(): { registry: HighlightRegistry; Highlight: HighlightConstructor } | null {
  const registry = (globalThis.CSS as unknown as { highlights?: HighlightRegistry } | undefined)?.highlights;
  const Highlight = (globalThis as unknown as { Highlight?: HighlightConstructor }).Highlight;
  return registry && Highlight ? { registry, Highlight } : null;
}

function paint(ranges: Range[], current: number): void {
  const api = highlightApi();
  if (!api) return;
  api.registry.set('sessions-find', new api.Highlight(...ranges));
  const active = ranges[current];
  if (active) api.registry.set('sessions-find-current', new api.Highlight(active));
  else api.registry.delete('sessions-find-current');
}

function clearPaint(): void {
  const api = highlightApi();
  api?.registry.delete('sessions-find');
  api?.registry.delete('sessions-find-current');
}

// ConversationFindBar searches the transcript the conversation has loaded and
// rendered, live as it changes, and walks the matches from the keyboard.
export function ConversationFindBar({ scroller, request, onClose }: Props): JSX.Element {
  const input = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');
  const [ranges, setRanges] = useState<Range[]>([]);
  const [current, setCurrent] = useState(0);
  const follow = useRef(true);

  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, [request]);

  // Search now, and again whenever the transcript changes: a streamed reply,
  // an expanded tool output, or earlier history that loaded.
  useEffect(() => {
    const root = scroller.current;
    if (!root) return undefined;
    let timer = 0;
    const search = (): void => {
      const next = findRanges(root, query);
      setRanges(next);
      setCurrent((index) => Math.min(index, Math.max(0, next.length - 1)));
    };
    follow.current = true;
    setCurrent(0);
    search();
    const observer = new MutationObserver(() => {
      window.clearTimeout(timer);
      timer = window.setTimeout(search, 150);
    });
    observer.observe(root, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ['open', 'class', 'hidden'] });
    return () => {
      window.clearTimeout(timer);
      observer.disconnect();
    };
  }, [query, scroller]);

  useEffect(() => {
    paint(ranges, current);
    const root = scroller.current;
    const target = ranges[current];
    if (root && target && follow.current) revealRange(root, target);
    follow.current = false;
  }, [ranges, current, scroller]);

  useEffect(() => clearPaint, []);

  // A conversation that leaves the screen takes its find bar with it.
  useEffect(() => {
    const root = scroller.current;
    if (!root || typeof IntersectionObserver === 'undefined') return undefined;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => !entry.isIntersecting && entry.target.getClientRects().length === 0)) onClose();
    });
    observer.observe(root);
    return () => observer.disconnect();
  }, [scroller, onClose]);

  const step = (direction: 1 | -1): void => {
    if (ranges.length === 0) return;
    follow.current = true;
    setCurrent((index) => (index + direction + ranges.length) % ranges.length);
  };

  const onKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>): void => {
    if (event.key === 'Escape') {
      event.preventDefault();
      onClose();
    } else if (event.key === 'Enter' || ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'g')) {
      event.preventDefault();
      step(event.shiftKey ? -1 : 1);
    }
  };

  const count = !query.trim() ? '' : ranges.length === 0 ? 'No matches' : `${current + 1} of ${ranges.length}`;
  return (
    <div className="conversation-find" role="search" aria-label="Find in conversation">
      <input
        ref={input}
        type="search"
        value={query}
        placeholder="Find in conversation"
        aria-label="Find in conversation"
        spellCheck={false}
        onChange={(event) => setQuery(event.target.value)}
        onKeyDown={onKeyDown}
      />
      <span className="conversation-find-count" aria-live="polite">{count}</span>
      <button type="button" aria-label="Previous match" title="Previous match (Shift+Enter)" disabled={ranges.length === 0} onClick={() => step(-1)}>↑</button>
      <button type="button" aria-label="Next match" title="Next match (Enter)" disabled={ranges.length === 0} onClick={() => step(1)}>↓</button>
      <button type="button" aria-label="Close find" title="Close (Escape)" onClick={onClose}>×</button>
    </div>
  );
}
