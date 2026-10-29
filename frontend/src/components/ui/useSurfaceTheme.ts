import { useLayoutEffect, useState, type CSSProperties, type RefObject } from 'react';

/** Body portals must inherit the same tokens as the operations shell. No stored theme of their own. */
export function useSurfaceTheme(root: RefObject<HTMLElement>): CSSProperties {
  const [style, setStyle] = useState<CSSProperties>({});
  useLayoutEffect(() => {
    const source = document.querySelector<HTMLElement>('.operations-shell');
    if (!source) return;
    const update = (): void => {
      const computed = getComputedStyle(source);
      const next: Record<string, string> = {};
      for (let index = 0; index < computed.length; index++) {
        const name = computed[index];
        if (name.startsWith('--')) next[name] = computed.getPropertyValue(name);
      }
      const inline = root.current && source.contains(root.current);
      next['--surface-scale'] = inline ? '1' : computed.getPropertyValue('--interface-scale') || '1';
      next['--surface-scale-inverse'] = computed.getPropertyValue('--interface-scale-inverse') || '1';
      setStyle(next);
    };
    update();
    const observer = new MutationObserver(update);
    observer.observe(source, { attributes: true, attributeFilter: ['class', 'data-theme', 'style'] });
    return () => observer.disconnect();
  }, [root]);
  return style;
}
