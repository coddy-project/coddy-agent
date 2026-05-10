import { expect, test } from 'vitest';
import { inMarkdownFenceBeforeCaret, slashMenuDraftAtCaret } from './draftSlash';

test('slashMenuDraftAtCaret basic prefix', () => {
  const s = slashMenuDraftAtCaret('/ab', 3);
  if (!s.open) {
    throw new Error('expected open draft');
  }
  expect(s.prefix).toBe('ab');
});

test('slashMenuDraftAtCaret inactive inside fence', () => {
  const text = '```\n/foo\n```\n';
  const mid = text.indexOf('/foo') + 1;
  expect(slashMenuDraftAtCaret(text, mid).open).toBe(false);
});

test('slashMenuDraftAtCaret inactive in blockquote', () => {
  expect(slashMenuDraftAtCaret('> /foo', 6).open).toBe(false);
});

test('slashMenuDraftAtCaret not at line start', () => {
  expect(slashMenuDraftAtCaret('x /foo', 6).open).toBe(false);
});

test('inMarkdownFenceBeforeCaret toggles on closed lines only', () => {
  const text = '```\nx\n';
  const caretAfterOpen = text.length;
  expect(inMarkdownFenceBeforeCaret(text, caretAfterOpen)).toBe(true);
});
