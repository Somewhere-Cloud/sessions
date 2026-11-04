// Find in conversation: a client-side search over the transcript a view has
// rendered. It reads the DOM the conversation already drew, so it finds what a
// person can see (an expanded tool output, yes; a collapsed one, no) and needs
// nothing from the daemon. `sessions grep` searches every conversation.

export const FIND_IN_CONVERSATION_EVENT = 'sessions:find-in-conversation';

// openConversationFind asks the conversation on screen to open its find bar.
export function openConversationFind(): void {
  window.dispatchEvent(new Event(FIND_IN_CONVERSATION_EVENT));
}

// isFindShortcut is Mod+F: Command on Apple platforms, Control elsewhere.
export function isFindShortcut(event: KeyboardEvent): boolean {
  if (event.altKey || event.shiftKey || event.key.toLowerCase() !== 'f') return false;
  const apple = /Mac|iPhone|iPad|iPod/.test(navigator.platform || navigator.userAgent);
  return apple ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey;
}

const BLOCKS = 'p,li,pre,td,th,h1,h2,h3,h4,h5,h6,summary,blockquote,dt,dd,div';
const SKIP = 'script,style,textarea,input,button,[aria-hidden="true"],.conversation-find';

interface Piece {
  node: Text;
  // Where this node's text starts in the joined, lowercased haystack.
  at: number;
}

function visibleText(root: HTMLElement): { haystack: string; pieces: Piece[] } {
  const pieces: Piece[] = [];
  const shown = new Map<Element, boolean>();
  const isShown = (element: Element): boolean => {
    let answer = shown.get(element);
    if (answer === undefined) {
      answer = !element.closest(SKIP) && element.getClientRects().length > 0;
      shown.set(element, answer);
    }
    return answer;
  };
  let haystack = '';
  let lastBlock: Element | null = null;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode() as Text | null; node; node = walker.nextNode() as Text | null) {
    const parent = node.parentElement;
    if (!parent || !node.data.trim() || !isShown(parent)) continue;
    const block = parent.closest(BLOCKS);
    // Text in different blocks never joins into one match.
    if (lastBlock && block !== lastBlock) haystack += '\n';
    lastBlock = block;
    const lower = node.data.toLowerCase();
    pieces.push({ node, at: haystack.length });
    haystack += lower.length === node.data.length ? lower : node.data;
  }
  return { haystack, pieces };
}

function locate(pieces: Piece[], offset: number, end: boolean): { node: Text; offset: number } {
  let low = 0;
  let high = pieces.length - 1;
  while (low < high) {
    const middle = Math.ceil((low + high) / 2);
    const piece = pieces[middle]!;
    if (piece.at < offset || (!end && piece.at === offset)) low = middle;
    else high = middle - 1;
  }
  const piece = pieces[low]!;
  return { node: piece.node, offset: Math.min(offset - piece.at, piece.node.data.length) };
}

// findRanges returns every case-insensitive match of query in the visible
// text under root, in document order, capped so a one-letter query on a long
// transcript stays responsive.
export function findRanges(root: HTMLElement, query: string, limit = 2000): Range[] {
  const needle = query.toLowerCase();
  if (!needle.trim()) return [];
  const { haystack, pieces } = visibleText(root);
  const ranges: Range[] = [];
  for (let index = haystack.indexOf(needle); index >= 0 && ranges.length < limit; index = haystack.indexOf(needle, index + needle.length)) {
    const start = locate(pieces, index, false);
    const end = locate(pieces, index + needle.length, true);
    const range = document.createRange();
    range.setStart(start.node, start.offset);
    range.setEnd(end.node, end.offset);
    ranges.push(range);
  }
  return ranges;
}

// revealRange scrolls the scroller so the range sits near its middle, unless
// it is already comfortably in view.
export function revealRange(scroller: HTMLElement, range: Range): void {
  const target = range.getBoundingClientRect();
  const view = scroller.getBoundingClientRect();
  const margin = Math.min(80, view.height / 4);
  if (target.top >= view.top + margin && target.bottom <= view.bottom - margin) return;
  scroller.scrollTop += target.top - view.top - view.height / 2 + target.height / 2;
}
