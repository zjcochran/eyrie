// SPDX-License-Identifier: AGPL-3.0-or-later
// A small Markdown reader for release notes: headings, lists, code blocks,
// paragraphs, and inline code, bold and links. It makes data, not HTML, so
// the page shows the notes as text whatever they hold.

export type Inline =
  | { kind: 'text' | 'code' | 'strong'; text: string }
  | { kind: 'link'; text: string; href: string };

export type Block =
  | { kind: 'heading'; level: number; text: Inline[] }
  | { kind: 'paragraph'; text: Inline[] }
  | { kind: 'list'; ordered: boolean; items: Inline[][] }
  | { kind: 'code'; text: string };

/** The blocks of a Markdown text. */
export function blocks(md: string): Block[] {
  const out: Block[] = [];
  const lines = md.replace(/\r\n?/g, '\n').split('\n');
  let para: string[] = [];
  const flush = () => {
    if (para.length) out.push({ kind: 'paragraph', text: inline(para.join(' ')) });
    para = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*```/.test(line)) {
      flush();
      const code: string[] = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) code.push(lines[i]);
      out.push({ kind: 'code', text: code.join('\n') });
      continue;
    }
    const h = /^(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    const item = /^\s*(?:([-*+])|(\d+)[.)])\s+(.*)$/.exec(line);
    if (h) {
      flush();
      out.push({ kind: 'heading', level: h[1].length, text: inline(h[2]) });
    } else if (item) {
      flush();
      const ordered = item[2] !== undefined;
      const last = out[out.length - 1];
      if (last?.kind === 'list' && last.ordered === ordered) last.items.push(inline(item[3]));
      else out.push({ kind: 'list', ordered, items: [inline(item[3])] });
    } else if (line.trim() === '') {
      flush();
    } else {
      para.push(line.trim());
    }
  }
  flush();
  return out;
}

/** The inline parts of a line: `code`, **bold**, [links](https://…) and bare https:// links. */
export function inline(s: string): Inline[] {
  const out: Inline[] = [];
  const re = /`([^`]+)`|\*\*(.+?)\*\*|\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g;
  let at = 0;
  for (const m of s.matchAll(re)) {
    if (m.index > at) out.push({ kind: 'text', text: s.slice(at, m.index) });
    if (m[1] !== undefined) out.push({ kind: 'code', text: m[1] });
    else if (m[2] !== undefined) out.push({ kind: 'strong', text: m[2] });
    else if (m[3] !== undefined) out.push({ kind: 'link', text: m[3], href: m[4] });
    else out.push({ kind: 'link', text: m[5], href: m[5] });
    at = m.index + m[0].length;
  }
  if (at < s.length) out.push({ kind: 'text', text: s.slice(at) });
  return out;
}
