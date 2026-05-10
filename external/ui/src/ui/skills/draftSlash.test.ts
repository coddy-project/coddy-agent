import { expect, test } from 'vitest';
import {
  draftExtendsFailedSlashPrefix,
  inMarkdownFenceBeforeCaret,
  slashMenuDraftAtCaret,
} from './draftSlash';

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

test('slashMenuDraftAtCaret works after whitespace mid-line', () => {
  const line = 'ljsdfa /fin';
  const s = slashMenuDraftAtCaret(line, line.length);
  if (!s.open) {
    throw new Error('expected open draft mid-line');
  }
  expect(s.prefix).toBe('fin');
});

test('slashMenuDraftAtCaret rejects slash after non-whitespace letter', () => {
  expect(slashMenuDraftAtCaret('x/foo', 4).open).toBe(false);
});

test('inMarkdownFenceBeforeCaret toggles on closed lines only', () => {
  const text = '```\nx\n';
  const caretAfterOpen = text.length;
  expect(inMarkdownFenceBeforeCaret(text, caretAfterOpen)).toBe(true);
});

test('draftExtendsFailedSlashPrefix extends longer token after failed prefix', () => {
  const s = '/adfadsfgaf';
  const d = slashMenuDraftAtCaret(s, s.length);
  if (!d.open) {
    throw new Error('expected open');
  }
  expect(draftExtendsFailedSlashPrefix(d, { slashIdx: 0, prefix: 'adf' })).toBe(true);
});

test('draftExtendsFailedSlashPrefix false when user shortened below failed prefix', () => {
  const d = slashMenuDraftAtCaret('/ad', 3);
  if (!d.open) {
    throw new Error('expected open');
  }
  expect(draftExtendsFailedSlashPrefix(d, { slashIdx: 0, prefix: 'adf' })).toBe(false);
});

test('draftExtendsFailedSlashPrefix empty failed prefix matches any token at slash', () => {
  const d = slashMenuDraftAtCaret('/hello', 6);
  if (!d.open) {
    throw new Error('expected open');
  }
  expect(draftExtendsFailedSlashPrefix(d, { slashIdx: 0, prefix: '' })).toBe(true);
});
