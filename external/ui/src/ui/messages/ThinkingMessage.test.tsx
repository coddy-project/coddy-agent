import React from 'react';
import { afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { expect, test } from 'vitest';
import { ThinkingMessage } from './ThinkingMessage';

afterEach(() => cleanup());

test('completed state uses plain thinking label without spinner', () => {
  const { container } = render(<ThinkingMessage status="completed" content="done reasoning" durationMs={12} />);
  expect(screen.getByText('thinking')).toBeTruthy();
  expect(screen.queryByText('thinking...')).toBeNull();
  expect(container.querySelector('.thinking-spinner')).toBeNull();
  const details = container.querySelector('details');
  expect(details).toBeTruthy();
  expect(details?.getAttribute('open')).toBeNull();
  expect(container.querySelector('.thinking-dur')?.textContent).toBe('12ms');
});

test('completed without duration shows placeholder in duration slot', () => {
  const { container } = render(<ThinkingMessage status="completed" content="x" />);
  expect(container.querySelector('.thinking-dur')?.textContent).toBe('-');
});

test('in_progress shows thinking ellipsis, spinner, and elapsed from startedAtMs', () => {
  const t0 = Date.now() - 2000;
  const { container } = render(<ThinkingMessage status="in_progress" content="" startedAtMs={t0} />);
  expect(screen.getByText('thinking...')).toBeTruthy();
  expect(container.querySelector('.thinking-spinner')).toBeTruthy();
  const dur = container.querySelector('.thinking-dur')?.textContent ?? '';
  expect(dur).toMatch(/^\d+ms$|^\d/);
});
