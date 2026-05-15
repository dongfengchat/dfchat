import React from 'react';

// Tiny chat-flavored Markdown subset. Renders to React nodes (never raw HTML)
// so XSS is impossible by construction. Mentions are handled by the caller
// after this transform; we just preserve their text.
//
// Supported:
//   **bold**
//   *italic*  / _italic_
//   `inline code`
//   ```code block```
//   > quote (line-start)
//   URLs auto-link

type Span = string | { type: 'b' | 'i' | 'code' | 'a'; text: string; href?: string };

function tokenizeInline(text: string): Span[] {
  const spans: Span[] = [];

  // Order matters: process code first (since it suppresses other markers).
  const re = /(`[^`\n]+?`)|(\*\*[^*\n]+?\*\*)|(__[^_\n]+?__)|(\*[^*\n]+?\*)|(_[^_\n]+?_)|((https?:\/\/[^\s)]+))/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    if (m.index > last) spans.push(text.slice(last, m.index));
    if (m[1]) {
      spans.push({ type: 'code', text: m[1].slice(1, -1) });
    } else if (m[2]) {
      spans.push({ type: 'b', text: m[2].slice(2, -2) });
    } else if (m[3]) {
      spans.push({ type: 'b', text: m[3].slice(2, -2) });
    } else if (m[4]) {
      spans.push({ type: 'i', text: m[4].slice(1, -1) });
    } else if (m[5]) {
      spans.push({ type: 'i', text: m[5].slice(1, -1) });
    } else if (m[7]) {
      spans.push({ type: 'a', text: m[7], href: m[7] });
    }
    last = re.lastIndex;
  }
  if (last < text.length) spans.push(text.slice(last));
  return spans;
}

function renderInlineWithMentions(text: string, mentionLookup: (handle: string) => boolean): React.ReactNode {
  const inline = tokenizeInline(text);
  return inline.map((span, i) => {
    if (typeof span === 'string') {
      return <React.Fragment key={i}>{renderMentions(span, mentionLookup)}</React.Fragment>;
    }
    if (span.type === 'b') return <strong key={i}>{renderMentions(span.text, mentionLookup)}</strong>;
    if (span.type === 'i') return <em key={i}>{renderMentions(span.text, mentionLookup)}</em>;
    if (span.type === 'code') {
      return (
        <code key={i} className="px-1 py-0.5 rounded bg-bg-1/60 border border-bg-5/40 text-[0.85em] font-mono">
          {span.text}
        </code>
      );
    }
    if (span.type === 'a') {
      return (
        <a key={i} href={span.href} target="_blank" rel="noreferrer" className="text-brand-300 underline hover:text-brand-200">
          {span.text}
        </a>
      );
    }
    return null;
  });
}

function renderMentions(text: string, isKnown: (handle: string) => boolean): React.ReactNode {
  const parts: React.ReactNode[] = [];
  const re = /@([A-Za-z0-9_]+)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let k = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    const handle = m[1];
    if (isKnown(handle)) {
      parts.push(
        <span key={`m-${k++}`} className="text-brand-300 bg-brand-500/15 rounded px-1 font-medium">
          @{handle}
        </span>,
      );
    } else {
      parts.push(`@${handle}`);
    }
    last = re.lastIndex;
  }
  if (last < text.length) parts.push(text.slice(last));
  return parts;
}

/**
 * Top-level renderer. Splits on triple-backtick code fences, blockquotes,
 * and blank lines, then renders inline markup inside each block.
 */
export function renderMarkdown(text: string, mentionLookup: (handle: string) => boolean): React.ReactNode {
  // First peel off triple-backtick fenced code.
  const blocks: React.ReactNode[] = [];
  const fence = /```([\s\S]*?)```/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let k = 0;

  function renderProseBlock(prose: string) {
    if (!prose) return;
    // Paragraph split on blank lines.
    const paragraphs = prose.split(/\n\s*\n/);
    for (const p of paragraphs) {
      const trimmed = p.replace(/^\n+|\n+$/g, '');
      if (!trimmed) continue;
      const isQuote = trimmed.split('\n').every((line) => line.startsWith('>'));
      if (isQuote) {
        const body = trimmed.split('\n').map((l) => l.replace(/^>\s?/, '')).join('\n');
        blocks.push(
          <blockquote
            key={`q-${k++}`}
            className="border-l-2 border-bg-5 pl-3 my-1 text-ink-3 whitespace-pre-wrap"
          >
            {renderInlineWithMentions(body, mentionLookup)}
          </blockquote>,
        );
      } else {
        blocks.push(
          <p key={`p-${k++}`} className="whitespace-pre-wrap break-words my-0.5">
            {renderInlineWithMentions(trimmed, mentionLookup)}
          </p>,
        );
      }
    }
  }

  while ((m = fence.exec(text))) {
    if (m.index > last) renderProseBlock(text.slice(last, m.index));
    blocks.push(
      <pre
        key={`code-${k++}`}
        className="my-1 rounded-lg bg-bg-1/60 border border-bg-5/40 p-2 text-[12px] font-mono overflow-x-auto whitespace-pre"
      >
        <code>{m[1].replace(/^\n+|\n+$/g, '')}</code>
      </pre>,
    );
    last = fence.lastIndex;
  }
  if (last < text.length) renderProseBlock(text.slice(last));

  return <>{blocks}</>;
}
