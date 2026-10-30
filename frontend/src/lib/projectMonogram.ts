// A project without a favicon is marked by two letters on a tint of its own,
// so projects tell apart at a glance: "emailsomewhere-refresh" is ER,
// "brightroom" is BR. The hue comes from the name, so it never changes.

export interface ProjectMonogram {
  letters: string;
  hue: number;
}

export function projectMonogram(name: string): ProjectMonogram {
  const trimmed = name.trim();
  const words = trimmed.replace(/([a-z])([A-Z])/g, '$1 $2').split(/[\s._/-]+/).filter(Boolean);
  const initial = (word: string | undefined): string => Array.from(word ?? '')[0] ?? '';
  const letters = words.length > 1
    ? initial(words[0]) + initial(words[1])
    : Array.from(words[0] ?? '').slice(0, 2).join('');
  let hash = 0;
  for (const char of trimmed.toLowerCase()) hash = (hash * 31 + char.codePointAt(0)!) >>> 0;
  return { letters: letters.toLocaleUpperCase() || '·', hue: hash % 360 };
}
