import { expect, test } from 'vitest';
import { segmentComposerMirrorSpans } from './composerMirrorSegments';

test('mirror chip only around caret slash token rest is plain text', () => {
  const s = 'asddf /foo /ba';
  const caret = s.length;
  const segs = segmentComposerMirrorSpans(s, caret, null);
  expect(segs).toEqual([
    { type: 'text', value: 'asddf /foo ' },
    { type: 'slash', literal: '/ba', name: 'ba' },
  ]);
});

test('mirror chip only middle token when caret inside it', () => {
  const s = 'a /bcd e';
  const slashPos = s.indexOf('/');
  const caret = slashPos + '/bc'.length;
  const segs = segmentComposerMirrorSpans(s, caret, null);
  expect(segs).toEqual([
    { type: 'text', value: 'a ' },
    { type: 'slash', literal: '/bc', name: 'bc' },
    { type: 'text', value: 'd e' },
  ]);
});

test('ignored slash tokens elsewhere are plain without caret', () => {
  expect(segmentComposerMirrorSpans('x /zzz y', 2, null)).toEqual([
    { type: 'text', value: 'x /zzz y' },
  ]);
});
